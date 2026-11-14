//! 打洞记录的只读缓存与回放。所有端口计划和网络结论均来自共享核心。
use serde_json::{Value, json};
use std::collections::{BTreeMap, HashMap};

pub fn text<'a>(value: &'a Value, key: &str) -> &'a str {
    value[key].as_str().unwrap_or_default()
}
pub fn number(value: &Value, key: &str) -> u64 {
    value[key]
        .as_str()
        .and_then(|v| v.parse().ok())
        .or_else(|| value[key].as_u64())
        .unwrap_or(0)
}

#[derive(Clone, Default)]
pub struct Record {
    pub run: Value,
    pub events: BTreeMap<u64, Value>,
    pub sequence: u64,
    pub dropped: String,
    pub evicted: String,
    pub more: bool,
    pub has_discovery: bool,
}
impl Record {
    pub fn merge(&mut self, reply: &Value) {
        let process = &reply["nat_punch_process"];
        let run = process["attempts"]
            .as_array()
            .and_then(|runs| runs.last())
            .cloned()
            .unwrap_or(Value::Null);
        let sequence = number(process, "sequence");
        if self.run["started_at"] != run["started_at"] || sequence < self.sequence {
            self.events.clear();
        }
        self.run = run;
        for event in process["events"].as_array().into_iter().flatten() {
            self.events.insert(number(event, "sequence"), event.clone());
        }
        // 与 PenMods 一致：分别保留阶段与报文，报文洪峰不能挤掉全部阶段。
        let mut packets = 0;
        let mut steps = 0;
        let mut keep = Vec::new();
        for (key, event) in self.events.iter().rev() {
            let packet = !text(event, "direction").is_empty() && !text(event, "packet").is_empty();
            let count = if packet { &mut packets } else { &mut steps };
            *count += 1;
            if *count <= if packet { 4096 } else { 512 } {
                keep.push(*key);
            }
        }
        self.events
            .retain(|key, _| keep.binary_search_by(|v| key.cmp(v)).is_ok());
        self.sequence = sequence;
        self.more = reply["more"] == true;
        self.has_discovery = reply["has_discovery"] == true;
        self.dropped = text(process, "events_dropped").into();
        self.evicted = text(process, "events_evicted").into();
    }

    /// 回放时从事件还原模型和 socket；不能用实时 run 覆盖历史节点。
    pub fn at(&self, count: usize) -> Value {
        let mut run = self.run.clone();
        if count >= self.events.len() {
            return run;
        }
        run["rounds"] = json!([]);
        run["sockets"] = json!([]);
        run["stage"] = json!("preparing");
        run["state"] = json!("running");
        run["finished_at"] = json!("");
        run["elapsed_ms"] = json!("0");
        let mut sockets: BTreeMap<String, Value> = BTreeMap::new();
        let mut rounds: BTreeMap<(u64, u64), Value> = BTreeMap::new();
        for event in self.events.values().take(count) {
            if !text(event, "stage").is_empty() {
                run["stage"] = event["stage"].clone();
            }
            run["elapsed_ms"] = event["elapsed_ms"].clone();
            if let Some(round) = event.get("round_snapshot").filter(|v| v.is_object()) {
                rounds.insert(
                    (number(round, "round"), number(round, "trial")),
                    round.clone(),
                );
            }
            let socket = text(event, "socket_id");
            if !socket.is_empty() {
                let entry = sockets
                    .entry(socket.into())
                    .or_insert_with(|| json!({"id":socket}));
                entry["round"] = event["round"].clone();
                entry["trial"] = event["trial"].clone();
                for (target, source) in [
                    ("bound_address", "bound_address"),
                    ("local_address", "local_address"),
                    ("mapped_address", "mapped_address"),
                    ("target", "remote_address"),
                    ("role", "socket_role"),
                ] {
                    if !text(event, source).is_empty() {
                        entry[target] = event[source].clone();
                    }
                }
                entry["state"] = event["kind"].clone();
                for (target, source) in [
                    ("local_address_source", "local_address_source"),
                    ("mapping_source", "address_source"),
                ] {
                    if !text(event, source).is_empty() {
                        entry[target] = event[source].clone();
                    }
                }
            }
        }
        run["rounds"] = Value::Array(rounds.into_values().collect());
        run["sockets"] = Value::Array(sockets.into_values().collect());
        run
    }
}

#[derive(Default)]
pub struct Process {
    pub transport: String,
    pub generation: String,
    pub follow_latest: bool,
    pub attempts: Vec<Value>,
    pub records: HashMap<(String, String), Record>,
    pub token: u64,
    pub pending: bool,
    pub error: String,
    pub paused: bool,
    pub position: usize,
    pub stream: String,
    pub selected_round: Option<(u64, u64)>,
    pub view: usize,
    pub filter: String,
    pub direction: String,
    pub packet: String,
    pub page: usize,
    pub target_page: usize,
    pub socket_page: usize,
    frozen: Option<Record>,
    next_stream: String,
}
impl Process {
    pub fn select(&mut self, transport: String, generation: String) {
        let token = self.token.wrapping_add(1);
        let follow_latest = generation.is_empty();
        let view = self.view;
        *self = Self {
            transport,
            generation,
            follow_latest,
            token,
            view,
            stream: if view == 3 { "discovery" } else { "connection" }.into(),
            ..Default::default()
        };
    }
    pub fn request(&mut self) -> Option<(u64, String, String, String, String)> {
        if self.transport.is_empty() || self.pending || self.paused {
            return None;
        }
        self.pending = true;
        self.token = self.token.wrapping_add(1);
        let stream = if self.next_stream.is_empty() {
            "connection"
        } else {
            &self.next_stream
        };
        let after = self
            .records
            .get(&(self.generation.clone(), stream.into()))
            .map(|record| record.sequence)
            .unwrap_or(0);
        let draining = self
            .records
            .get(&(self.generation.clone(), stream.into()))
            .is_some_and(|r| r.more);
        let generation = if self.follow_latest && !draining && stream == "connection" {
            String::new()
        } else {
            self.generation.clone()
        };
        Some((
            self.token,
            self.transport.clone(),
            after.to_string(),
            generation,
            stream.into(),
        ))
    }
    pub fn accept(&mut self, token: u64, result: Result<Value, String>) -> bool {
        if !self.pending || token != self.token {
            return false;
        }
        self.pending = false;
        match result {
            Err(error) => self.error = error,
            Ok(reply) => {
                self.error.clear();
                if let Some(attempts) = reply["attempts"].as_array() {
                    self.attempts = attempts.clone();
                }
                let generation = text(&reply, "selected_generation").to_string();
                if self.follow_latest || self.generation.is_empty() {
                    self.generation = generation.clone();
                }
                let stream = text(&reply, "stream").to_string();
                if reply["nat_punch_process"]["attempts"]
                    .as_array()
                    .is_some_and(|runs| !runs.is_empty())
                {
                    self.records
                        .entry((generation, stream.clone()))
                        .or_default()
                        .merge(&reply);
                }
                self.records.retain(|(generation, _), _| {
                    self.attempts
                        .iter()
                        .any(|a| text(a, "generation") == generation)
                });
                self.next_stream = if reply["more"] == true {
                    stream
                } else if stream == "connection" && reply["has_discovery"] == true {
                    "discovery".into()
                } else {
                    "connection".into()
                };
            }
        }
        true
    }
    pub fn record(&self) -> Option<&Record> {
        if self.paused {
            return self.frozen.as_ref();
        }
        self.records.get(&(
            self.generation.clone(),
            if self.stream.is_empty() {
                "connection".into()
            } else {
                self.stream.clone()
            },
        ))
    }
    pub fn pause(&mut self) -> bool {
        // 分页未读完时继续读取，不能把不完整的窗口误当作全部记录。
        if self.pending || self.record().is_none_or(|r| r.more) {
            return false;
        }
        self.frozen = self.record().cloned();
        self.position = self.frozen.as_ref().map(|r| r.events.len()).unwrap_or(0);
        self.paused = true;
        true
    }
    pub fn live(&mut self) {
        self.paused = false;
        self.frozen = None;
        self.page = 0;
    }
    pub fn count(&self) -> usize {
        self.record()
            .map(|r| {
                if self.paused {
                    self.position.min(r.events.len())
                } else {
                    r.events.len()
                }
            })
            .unwrap_or(0)
    }
    pub fn events(&self) -> Vec<&Value> {
        self.record()
            .map(|record| {
                record
                    .events
                    .values()
                    .take(self.count())
                    .filter(|event| {
                        let round = self.selected_round.is_none_or(|(round, trial)| {
                            number(event, "round") == round && number(event, "trial") == trial
                        });
                        let direction =
                            self.direction.is_empty() || text(event, "direction") == self.direction;
                        let packet = self.packet.is_empty()
                            || text(event, "packet").eq_ignore_ascii_case(&self.packet);
                        round
                            && direction
                            && packet
                            && (self.filter.is_empty()
                                || event
                                    .to_string()
                                    .to_lowercase()
                                    .contains(&self.filter.to_lowercase()))
                    })
                    .collect()
            })
            .unwrap_or_default()
    }
}

pub fn stage(value: &str) -> &str {
    match value {
        "preparing" => "准备连接",
        "ice" => "ICE 候选检查",
        "waiting_nat" => "等待 NAT 采样",
        "strategy" => "选择打洞策略",
        "reservation" => "预约打洞时段",
        "model" => "交换 NAT 模型",
        "model_refresh" => "刷新分配模型",
        "binding" => "预绑定 socket",
        "calibration_wait" => "同步校准准备",
        "calibration" => "校准公网映射",
        "coordination" => "校验双方计划",
        "udp" => "UDP 端口探测",
        "quic" => "QUIC 握手",
        "mux" => "身份与通道协商",
        "connected" => "业务通道就绪",
        "nat_sampling" => "NAT 映射与过滤采样",
        _ => value,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn reply(sequence: &str, start: &str, more: bool) -> Value {
        json!({"selected_generation":"3","stream":"connection","more":more,"attempts":[{"generation":"3"}],
            "nat_punch_process":{"sequence":sequence,"attempts":[{"started_at":start}],"events":[{"sequence":sequence,"title":"记录"}]}})
    }
    #[test]
    fn reconnect_deduplicates_and_reset_drops_old_records_without_rounding_u64() {
        let mut record = Record::default();
        record.merge(&reply("9007199254740993", "one", false));
        record.merge(&reply("9007199254740994", "one", false));
        record.merge(&reply("9007199254740994", "one", false));
        assert_eq!(record.events.len(), 2);
        record.merge(&reply("1", "two", false));
        assert_eq!(record.events.keys().copied().collect::<Vec<_>>(), [1]);
    }
    #[test]
    fn stale_responses_and_incomplete_pages_cannot_freeze_or_replace_current_device() {
        let mut process = Process::default();
        process.select("first".into(), "3".into());
        let old = process.request().unwrap().0;
        process.select("second".into(), "3".into());
        process.request();
        assert!(!process.accept(old, Ok(reply("1", "one", false))));
        let token = process.token;
        assert!(process.accept(token, Ok(reply("1", "one", true))));
        assert!(!process.pause());
        let token = process.request().unwrap().0;
        process.accept(token, Ok(reply("2", "one", false)));
        assert!(process.pause());
        process.position = 1;
        assert_eq!(process.count(), 1);
        assert!(process.request().is_none());
    }
    #[test]
    fn following_latest_keeps_page_cursors_pinned_then_observes_new_attempts() {
        let mut process = Process::default();
        process.select("peer".into(), String::new());
        let request = process.request().unwrap();
        assert_eq!(request.3, "");
        process.accept(request.0, Ok(reply("256", "one", true)));
        let request = process.request().unwrap();
        assert_eq!(request.2, "256");
        assert_eq!(request.3, "3");
        process.accept(request.0, Ok(reply("300", "one", false)));
        let request = process.request().unwrap();
        assert_eq!(request.3, "");
        let mut next = reply("301", "two", false);
        next["selected_generation"] = json!("4");
        next["attempts"] = json!([{"generation":"3"},{"generation":"4"}]);
        process.accept(request.0, Ok(next));
        assert_eq!(process.generation, "4");
        assert_eq!(process.record().unwrap().events.len(), 1);
    }
    #[test]
    fn replay_does_not_leak_final_state_or_future_models_and_sockets() {
        let mut record = Record::default();
        let mut response = reply("3", "one", false);
        response["nat_punch_process"]["attempts"][0]["stage"] = json!("connected");
        response["nat_punch_process"]["attempts"][0]["state"] = json!("succeeded");
        response["nat_punch_process"]["events"] = json!([
            {"sequence":"1","elapsed_ms":"20","stage":"binding","round_snapshot":{"round":1,"trial":2,"selected_socket":""}},
            {"sequence":"2","elapsed_ms":"30","round":1,"trial":2,"socket_id":"udp-1","bound_address":"0.0.0.0:4000","kind":"socket_bound"},
            {"sequence":"3","elapsed_ms":"40","stage":"connected","kind":"socket_selected","socket_id":"udp-1"}]);
        record.merge(&response);
        let start = record.at(0);
        assert_eq!(start["elapsed_ms"], "0");
        assert_eq!(start["state"], "running");
        let middle = record.at(2);
        assert_eq!(middle["stage"], "binding");
        assert_eq!(middle["sockets"][0]["round"], 1);
        assert_eq!(record.at(3)["stage"], "connected");
    }
}
