//! Session-only presentation state, keyed independently of snapshot order.
use crate::{App, MainWindow, MappingRow, PeerRow};
use slint::{ComponentHandle, Model, ModelRc, VecModel};
use std::{cell::RefCell, collections::HashSet};

thread_local! {
    static EXPANDED: RefCell<HashSet<String>> = RefCell::new(HashSet::new());
}

pub fn bind(window: &MainWindow) {
    window.global::<App>().on_is_expanded(|key| EXPANDED.with(|s| s.borrow().contains(key.as_str())));
    let weak = window.as_weak();
    window.global::<App>().on_set_expanded(move |key, expanded| {
        EXPANDED.with(|s| {
            if expanded { s.borrow_mut().insert(key.to_string()); }
            else { s.borrow_mut().remove(key.as_str()); }
        });
        if let Some(w) = weak.upgrade() {
            let app = w.global::<App>();
            app.set_expansion_revision(app.get_expansion_revision().wrapping_add(1));
        }
    });
}

/// Keep the model object and unaffected rows alive; reorder only the moved IDs.
pub fn reconcile<T: Clone + 'static>(model: ModelRc<T>, rows: Vec<T>, key: impl Fn(&T) -> String) -> ModelRc<T> {
    let Some(existing) = model.as_any().downcast_ref::<VecModel<T>>() else {
        return ModelRc::new(VecModel::from(rows));
    };
    for (index, row) in rows.iter().enumerate() {
        let id = key(row);
        if existing.row_data(index).is_some_and(|old| key(&old) == id) {
            existing.set_row_data(index, row.clone());
            continue;
        }
        if let Some(old) = (index + 1..existing.row_count())
            .find(|&i| key(&existing.row_data(i).unwrap()) == id) {
            existing.remove(old);
        }
        existing.insert(index, row.clone());
    }
    while existing.row_count() > rows.len() { existing.remove(rows.len()); }
    model
}

pub fn update(window: &MainWindow, provides: Vec<MappingRow>, consumes: Vec<MappingRow>, runtime: Vec<MappingRow>, peers: Vec<PeerRow>) {
    let app = window.global::<App>();
    let keys: HashSet<String> = peers.iter().map(|p| format!("peer:{}", p.peer_id))
        .chain(runtime.iter().map(|m| format!("runtime:{}:{}", m.role, m.id))).collect();
    EXPANDED.with(|s| s.borrow_mut().retain(|key| keys.contains(key)));
    app.set_expansion_revision(app.get_expansion_revision().wrapping_add(1));
    if !peers.iter().any(|p| p.peer_id == app.get_selected_peer()) {
        app.set_selected_peer(peers.first().map(|p| p.peer_id.clone()).unwrap_or_default());
    }
    app.set_provides(reconcile(app.get_provides(), provides, |m| m.entry_id.to_string()));
    app.set_consumes(reconcile(app.get_consumes(), consumes, |m| m.entry_id.to_string()));
    app.set_runtime_mappings(reconcile(app.get_runtime_mappings(), runtime, |m| format!("{}:{}", m.role, m.id)));
    app.set_peers(reconcile(app.get_peers(), peers, |p| p.peer_id.to_string()));
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn refresh_reorder_insert_and_remove_keep_model_identity() {
        let original = ModelRc::new(VecModel::from(vec!["a".to_string(), "b".into()]));
        let next = reconcile(original.clone(), vec!["b".into(), "c".into(), "a".into()], Clone::clone);
        assert!(std::ptr::eq(original.as_any(), next.as_any()));
        assert_eq!(next.iter().collect::<Vec<_>>(), ["b", "c", "a"]);
        let next = reconcile(next, vec!["c".into()], Clone::clone);
        assert_eq!(next.iter().collect::<Vec<_>>(), ["c"]);
    }
}
