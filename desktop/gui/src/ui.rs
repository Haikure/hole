//! 桌面视觉语言：无描边表面、柔和投影、圆角与内嵌 SVG。
use gpui::{
    AssetSource, BoxShadow, Div, Hsla, SharedString, Svg, div, point, prelude::*, px, rgb,
    rgb_to_hsla, rgba, svg,
};
use std::{
    borrow::Cow,
    sync::{Arc, OnceLock},
};
use yororen_ui::{assets::UiAsset, renderer, theme::Theme};

#[derive(Clone, Copy)]
pub struct Palette {
    pub bg: Hsla,
    pub sidebar: Hsla,
    pub surface: Hsla,
    pub inset: Hsla,
    pub dock: Hsla,
    pub text: Hsla,
    pub muted: Hsla,
    pub accent: Hsla,
    pub soft: Hsla,
    pub on_accent: Hsla,
    pub good: Hsla,
    pub warn: Hsla,
    pub bad: Hsla,
    pub dark: bool,
}
impl Palette {
    pub fn new(dark: bool, accent: &str) -> Self {
        // 与 Android HoleTheme.kt 中的 Material 色阶保持一致。
        let (light, strong, soft, deep, on_dark) = match accent {
            "teal" | "green" => (0x386B4B, 0x9BD5A9, 0xD0EAD4, 0x31583D, 0x173622),
            "violet" | "purple" => (0x66508D, 0xD0B9FF, 0xE9DDFF, 0x503B78, 0x35205C),
            "rose" => (0x98445F, 0xFFB1C7, 0xFFD9E2, 0x79334A, 0x5A1930),
            "amber" => (0x805610, 0xF2BF6B, 0xFFDDA8, 0x634400, 0x442C00),
            _ => (0x435E91, 0xADC6FF, 0xD8E2FF, 0x294677, 0x102F60),
        };
        Self {
            bg: rgb_to_hsla(rgb(if dark { 0x121216 } else { 0xFAF9FC })),
            sidebar: rgb_to_hsla(rgb(if dark { 0x121216 } else { 0xFAF9FC })),
            surface: rgb_to_hsla(rgb(if dark { 0x1C1B20 } else { 0xF3F2F7 })),
            inset: rgb_to_hsla(rgb(if dark { 0x2B2930 } else { 0xE8E7ED })),
            dock: rgb_to_hsla(rgb(if dark { 0x201F25 } else { 0xEEEEF3 })),
            text: rgb_to_hsla(rgb(if dark { 0xE5E1E9 } else { 0x1B1B20 })),
            muted: rgb_to_hsla(rgb(if dark { 0xCBC5D1 } else { 0x48474F })),
            accent: rgb_to_hsla(rgb(if dark { strong } else { light })),
            soft: rgb_to_hsla(rgb(if dark { deep } else { soft })),
            on_accent: rgb_to_hsla(rgb(if dark { on_dark } else { 0xFFFFFF })),
            good: rgb_to_hsla(rgb(if dark { 0x9BD5A9 } else { 0x238461 })),
            warn: rgb_to_hsla(rgb(if dark { 0xF2BF6B } else { 0x98630F })),
            bad: rgb_to_hsla(rgb(if dark { 0xFFB1B1 } else { 0xBB4051 })),
            dark,
        }
    }
    pub fn install(self, cx: &mut gpui::App, reduce_motion: bool) {
        let mut theme = if self.dark {
            renderer::system_dark()
        } else {
            renderer::system_light()
        };
        let color = |c: Hsla| serde_json::json!({"h":c.hue.into_positive_degrees()/360.,"s":c.saturation,"l":c.lightness,"a":c.alpha});
        for (key, value) in [
            ("surface.base", self.surface),
            ("surface.canvas", self.bg),
            ("surface.raised", self.surface),
            ("surface.sunken", self.inset),
            ("surface.hover", self.inset),
            ("content.primary", self.text),
            ("content.secondary", self.muted),
            ("content.tertiary", self.muted),
            ("border.default", self.inset),
            ("border.muted", self.inset),
            ("border.focus", self.accent),
            ("action.primary.bg", self.accent),
            ("action.primary.hover_bg", self.accent),
            ("action.primary.active_bg", self.accent),
            ("action.primary.fg", self.on_accent),
            ("action.neutral.bg", self.inset),
            ("action.primary.disabled_bg", self.inset),
            ("action.primary.disabled_fg", self.muted),
            ("action.neutral.hover_bg", self.soft),
            ("action.neutral.fg", self.text),
        ] {
            theme.set(key, color(value));
        }
        for (key, value) in [
            ("tokens.radii.md", 12.),
            ("tokens.control.button.radius", 14.),
            ("tokens.control.button.min_height", 40.),
            ("tokens.control.input.min_height", 42.),
            ("tokens.control.input.horizontal_padding", 14.),
            ("tokens.control.input.vertical_padding", 10.),
            ("tokens.control.input.radius", 12.),
            ("tokens.control.switch.track_w", 44.),
            ("tokens.control.switch.track_h", 26.),
            ("tokens.control.switch.knob_size", 20.),
            ("tokens.control.button.radius", 22.),
        ] {
            theme.set(key, serde_json::json!(value));
        }
        if reduce_motion {
            for key in [
                "tokens.motion.duration_fast",
                "tokens.motion.duration_normal",
                "tokens.motion.duration_slow",
            ] {
                theme.set(key, serde_json::json!(0));
            }
        }
        cx.set_reduce_motion(reduce_motion);
        yororen_ui::theme::install(cx, Theme::from_value(theme.0));
    }
}
pub fn col() -> Div {
    div().flex().flex_col().gap_4().min_w_0()
}
pub fn row() -> Div {
    div().flex().items_center().gap_3().min_w_0()
}
pub fn card(p: Palette) -> Div {
    col()
        .p_5()
        .rounded(px(20.))
        .bg(surface_gradient(p))
        .shadow(vec![BoxShadow {
            color: rgba(if p.dark { 0x071B3C47 } else { 0x071B3C14 }).into(),
            offset: point(px(0.), px(4.)),
            blur_radius: px(10.),
            spread_radius: px(0.),
            inset: false,
        }])
}
pub fn blend(a: Hsla, b: Hsla, t: f32) -> Hsla {
    let a = gpui::hsla_to_rgba(a);
    let b = gpui::hsla_to_rgba(b);
    rgb_to_hsla(gpui::Rgba::new(
        a.red + (b.red - a.red) * t,
        a.green + (b.green - a.green) * t,
        a.blue + (b.blue - a.blue) * t,
        a.alpha + (b.alpha - a.alpha) * t,
    ))
}
pub fn surface_gradient(p: Palette) -> gpui::Background {
    gpui::linear_gradient(
        135.,
        gpui::linear_color_stop(
            blend(
                p.surface,
                rgb_to_hsla(rgb(0xFFFFFF)),
                if p.dark { 0.045 } else { 0.70 },
            ),
            0.,
        ),
        gpui::linear_color_stop(p.surface, 1.),
    )
}
pub fn dock_shadow(p: Palette) -> Vec<BoxShadow> {
    vec![
        BoxShadow {
            color: rgba(if p.dark { 0x0000003D } else { 0x00000014 }).into(),
            offset: point(px(0.), px(8.)),
            blur_radius: px(24.),
            spread_radius: px(0.),
            inset: false,
        },
        BoxShadow {
            color: rgba(if p.dark { 0x0000001F } else { 0x0000000A }).into(),
            offset: point(px(0.), px(1.)),
            blur_radius: px(3.),
            spread_radius: px(0.),
            inset: false,
        },
    ]
}
pub fn status_palette(mut p: Palette, kind: &str) -> Palette {
    let tone = rgb_to_hsla(rgb(match kind {
        "good" => 0x17624F,
        "bad" => 0x9B3039,
        "warn" => 0x80541A,
        _ => 0x364256,
    }));
    p.surface = tone;
    p.inset = blend(tone, gpui::white(), 0.12);
    p.soft = blend(tone, gpui::white(), 0.12);
    p.text = gpui::white();
    p.muted = blend(tone, gpui::white(), 0.86);
    p.accent = gpui::white();
    p
}
pub const APP_ICON: &[u8] = include_bytes!("../ui/assets/app-icon.png");
pub fn app_icon() -> Arc<gpui::Image> {
    static ICON: OnceLock<Arc<gpui::Image>> = OnceLock::new();
    ICON.get_or_init(|| {
        Arc::new(gpui::Image::from_bytes(
            gpui::ImageFormat::Png,
            APP_ICON.to_vec(),
        ))
    })
    .clone()
}
pub fn heading(
    title: impl Into<SharedString>,
    subtitle: impl Into<SharedString>,
    p: Palette,
) -> Div {
    let subtitle = subtitle.into();
    col()
        .gap_2()
        .child(
            div()
                .text_2xl()
                .font_weight(gpui::FontWeight::SEMIBOLD)
                .child(title.into()),
        )
        .children(
            (!subtitle.is_empty()).then(|| div().text_sm().text_color(p.muted).child(subtitle)),
        )
}
pub fn icon(name: &str, color: Hsla, size: f32) -> Svg {
    svg()
        .path(icon_path(name))
        .size(px(size))
        .text_color(color)
        .flex_shrink_0()
}
pub fn builtin_icon(name: &str) -> Option<&'static str> {
    match name {
        "check" => Some("check"),
        "close" => Some("close"),
        "edit" => Some("pencil"),
        "trash" => Some("trash"),
        "search" => Some("search"),
        "info" => Some("info"),
        "chevron" => Some("arrow-right"),
        "back" => Some("arrow-left"),
        _ => None,
    }
}
pub fn icon_path(name: &str) -> SharedString {
    builtin_icon(name)
        .map(|name| format!("icons/{name}.svg"))
        .unwrap_or_else(|| format!("hole/{name}.svg"))
        .into()
}
pub fn badge(text: impl Into<SharedString>, color: Hsla, p: Palette) -> Div {
    row()
        .gap_2()
        .px_3()
        .py_1()
        .rounded_full()
        .bg(p.inset)
        .text_xs()
        .text_color(color)
        .child(div().size(px(6.)).rounded_full().bg(color))
        .child(text.into())
}
pub fn fact(label: impl Into<SharedString>, value: impl Into<SharedString>, p: Palette) -> Div {
    col()
        .gap_1()
        .min_w_0()
        .child(div().text_xs().text_color(p.muted).child(label.into()))
        .child(div().text_sm().child(value.into()))
}
pub fn metric(name: &str, label: &str, value: String, detail: &str, p: Palette) -> Div {
    card(p)
        .flex_1()
        .gap_3()
        .child(
            row()
                .justify_between()
                .child(div().text_sm().text_color(p.muted).child(label.to_string()))
                .child(
                    div()
                        .p_2()
                        .rounded(px(12.))
                        .bg(p.soft)
                        .child(icon(name, p.accent, 20.)),
                ),
        )
        .child(
            div()
                .text_3xl()
                .font_weight(gpui::FontWeight::SEMIBOLD)
                .child(value),
        )
        .children((!detail.is_empty()).then(|| {
            div()
                .text_xs()
                .text_color(p.muted)
                .child(detail.to_string())
        }))
}
pub fn empty(name: &str, title: &str, detail: &str, p: Palette) -> Div {
    col()
        .items_center()
        .justify_center()
        .py_8()
        .gap_3()
        .child(
            div()
                .p_3()
                .rounded(px(16.))
                .bg(p.soft)
                .child(icon(name, p.accent, 24.)),
        )
        .child(div().text_lg().child(title.to_string()))
        .children((!detail.is_empty()).then(|| {
            div()
                .text_sm()
                .text_color(p.muted)
                .max_w(px(520.))
                .child(detail.to_string())
        }))
}

const ICONS: &[(&str, &str)] = &[
    (
        "home",
        "M3 10 12 3l9 7v10a1 1 0 0 1-1 1h-5v-7H9v7H4a1 1 0 0 1-1-1Z",
    ),
    (
        "services",
        "M5 7h14M5 12h14M5 17h9M3 5v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2Z",
    ),
    ("devices", "M3 4h18v12H3ZM8 20h8M12 16v4"),
    (
        "voice",
        "M9 5a3 3 0 0 1 6 0v7a3 3 0 0 1-6 0ZM5 10v2a7 7 0 0 0 14 0v-2M12 19v3M8 22h8",
    ),
    (
        "settings",
        "M9 3h6l1 3 3 1 2 5-2 5-3 1-1 3H9l-1-3-3-1-2-5 2-5 3-1ZM15 12a3 3 0 1 1-6 0 3 3 0 0 1 6 0Z",
    ),
    (
        "network",
        "M10 3h4a1 1 0 0 1 1 1v3a1 1 0 0 1-1 1h-4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1ZM3 16h4a1 1 0 0 1 1 1v3a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1v-3a1 1 0 0 1 1-1ZM17 16h4a1 1 0 0 1 1 1v3a1 1 0 0 1-1 1h-4a1 1 0 0 1-1-1v-3a1 1 0 0 1 1-1ZM12 8v4M5 16v-4h14v4",
    ),
    (
        "link",
        "M10 13a4 4 0 0 0 6 0l3-3a4.25 4.25 0 0 0-6-6l-2 2M14 11a4 4 0 0 0-6 0l-3 3a4.25 4.25 0 0 0 6 6l2-2",
    ),
    ("activity", "M2 12h5l3-8 4 16 3-8h5"),
    ("play", "m9 5 11 7-11 7Z"),
    ("stop", "M6 6h12v12H6Z"),
    ("arrow-up", "M12 20V4m-6 6 6-6 6 6"),
    ("arrow-down", "M12 4v16m-6-6 6 6 6-6"),
    (
        "send",
        "M12 16V3m-5 5 5-5 5 5M4 16v3a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-3",
    ),
    (
        "receive",
        "M12 3v13m-5-5 5 5 5-5M4 16v3a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-3",
    ),
    ("plus", "M12 5v14M5 12h14"),
    ("edit", "m14 5 5 5M4 20l4-1L21 6l-4-4L4 15Z"),
    (
        "trash",
        "M3 6h18M9 6V3h6v3M6 6l1 15h10l1-15M10 10v7M14 10v7",
    ),
    ("check", "m5 12 4 4L19 6"),
    ("close", "m6 6 12 12M6 18 18 6"),
    (
        "refresh",
        "M3 12a9 9 0 0 1 15.36-6.36L21 8M21 3v5h-5M21 12a9 9 0 0 1-15.36 6.36L3 16M3 21v-5h5",
    ),
    (
        "route",
        "M5 5h5a4 4 0 0 1 0 8H7a4 4 0 0 0 0 8h12M2 5a3 3 0 1 0 6 0 3 3 0 0 0-6 0ZM16 21a3 3 0 1 0 6 0 3 3 0 0 0-6 0Z",
    ),
    (
        "shield",
        "m12 3 9 4v5c0 5-9 10-9 10S3 17 3 12V7ZM8 12l3 3 5-6",
    ),
    ("copy", "M9 8h12v13H9ZM15 8V3H3v13h6"),
    ("download", "M12 3v12m-5-5 5 5 5-5M4 17v4h16v-4"),
    ("upload", "M12 16V4m-5 5 5-5 5 5M4 17v4h16v-4"),
    (
        "terminal",
        "M5 3h14a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2Zm2 5 4 4-4 4m7 0h3",
    ),
    (
        "consume",
        "M7 5V4a1 1 0 0 1 1-1h12a1 1 0 0 1 1 1v11a1 1 0 0 1-1 1H8a1 1 0 0 1-1-1v-1M14 16v5m-4 0h8M2 10h11m-4-4 4 4-4 4",
    ),
    (
        "share",
        "M18 3a3 3 0 1 1 0 6 3 3 0 0 1 0-6ZM6 9a3 3 0 1 1 0 6 3 3 0 0 1 0-6ZM18 15a3 3 0 1 1 0 6 3 3 0 0 1 0-6ZM9 11l6-4M9 13l6 4",
    ),
    ("clock", "M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0ZM12 7v5l3 2"),
    ("search", "M16 10a6 6 0 1 1-12 0 6 6 0 0 1 12 0Zm-2 5 7 7"),
    (
        "sun",
        "M16 12a4 4 0 1 1-8 0 4 4 0 0 1 8 0ZM12 2v2M12 20v2M2 12h2M20 12h2M5 5l1 1M18 18l1 1M5 19l1-1M18 6l1-1",
    ),
    ("moon", "M21 14A9 9 0 0 1 10 3a9 9 0 1 0 11 11Z"),
    (
        "info",
        "M12 11v6M12 7h.01M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z",
    ),
    ("chevron", "m9 5 7 7-7 7"),
    ("back", "m15 5-7 7 7 7"),
    ("pause", "M8 5v14M16 5v14"),
    ("packet", "m3 7 9-4 9 4v10l-9 4-9-4ZM3 7l9 5 9-5M12 12v9"),
    ("grid", "M3 3h7v7H3ZM14 3h7v7h-7ZM3 14h7v7H3ZM14 14h7v7h-7Z"),
    ("filter", "M3 4h18l-7 8v7l-4 2v-9Z"),
];
pub struct Assets;
impl AssetSource for Assets {
    fn load(&self, path: &str) -> gpui::Result<Option<Cow<'static, [u8]>>> {
        if let Some(name) = path
            .strip_prefix("hole/")
            .and_then(|p| p.strip_suffix(".svg"))
        {
            if let Some((_, d)) = ICONS.iter().find(|(key, _)| *key == name) {
                return Ok(Some(Cow::Owned(format!(r#"<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><path d="{d}" fill="none" stroke="black" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg>"#).into_bytes())));
            }
        }
        UiAsset.load(path)
    }
    fn list(&self, path: &str) -> gpui::Result<Vec<SharedString>> {
        let mut assets = UiAsset.list(path)?;
        assets.extend(
            ICONS
                .iter()
                .map(|(key, _)| SharedString::from(format!("hole/{key}.svg")))
                .filter(|name| name.starts_with(path)),
        );
        Ok(assets)
    }
}
