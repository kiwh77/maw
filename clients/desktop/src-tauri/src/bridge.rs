//! 桌面壳的桥接**判据**：导航白名单、事件分类与去重、令牌脱敏、SSE／feed 解析。
//!
//! 这里全是纯函数／纯结构，不碰 Tauri 运行时也不起进程，因此可以被单元测试直接钉住。
//! 真正的 I/O（订阅事件流、补拉 feed、弹系统通知）在 `pump.rs` 里，只调用本模块的判据。
//!
//! 三条边界（VN-902 验收）：
//!
//! 1. **桥接只允许已声明的通知与导航。** 通知只来自 [`reminder::classify_value`] 里那张封闭表；
//!    表外的事件一律不弹窗——事件账本里绝大多数记录（`progress`、`claimed`、`merged`…）
//!    与「卡在人手上」无关，把它们都弹出来就是打扰。
//! 2. **权限被拒时保留应用内提醒。** 桌面投递是「附加渠道」：站内记录由 hub 独立持有，
//!    桌面这边拒投、失败或重复都只是「这次不弹」，不得回写、不得确认、不得假装已送达。
//! 3. **令牌不放 URL 或日志。** 导航只去 hub 自身 origin，且带敏感键的 URL 直接拒绝；
//!    任何要打印的外来文本都先过 [`redact::scrub`]。
//!
//! 事件来源以 hub（`127.0.0.1:7420`）为准，共两条：
//! `GET /api/events` 的 SSE（`event: event` 带正文，`catchup` 表示积压超限），
//! 以及 `GET /api/feed?since=N` 的游标补拉。**帧名不是事实，事件正文才是**：
//! 断线、积压、重启都靠游标补拉补回来，SSE 丢失不构成「没发生」。

use std::time::Duration;

use tauri::Url;

/// hub 的 origin。桌面壳只认这一个来源：既不做代理，也不接受外部跳转。
pub const HUB_ORIGIN: &str = "http://127.0.0.1:7420";
/// hub 端口（与 `main.rs` 的 `HUB_ADDR` 同源）。
pub const HUB_PORT: u16 = 7420;

/// 已确认提醒的去重窗口，与 hub 侧 `notifications.DefaultDedupWindow` 同一口径。
///
/// 窗口内同一对象同一状态再次出现，说明用户刚处理过，不再弹；窗口外再出现
/// 就算一次新的介入需求。桌面壳只照抄这个时长，不自己另立一套。
pub const DEDUP_WINDOW: Duration = Duration::from_secs(30 * 60);

/// 导航白名单：只允许「回到本机 hub」这一种跳转。
pub mod nav {
    use super::{Url, HUB_PORT};

    /// 拒绝原因。每个取值都能单独解释清楚，不用一句「不允许」糊过去。
    #[derive(Debug, Clone, Copy, PartialEq, Eq)]
    pub enum Denied {
        /// 只有 http 被接受：https 会让本机 hub 的证书成为另一个问题。
        Scheme,
        /// 只接受回环地址：跳去公网页面等于把桌面壳变成浏览器。
        Host,
        /// 只接受 hub 端口：同机其它端口不属于本应用。
        Port,
        /// URL 里不能带用户信息（`user:pass@`）。
        UserInfo,
        /// URL 里出现令牌类参数：契约明令禁止令牌进 URL。
        TokenInUrl,
    }

    impl Denied {
        /// 给日志用的一句话（**不含 URL 原文**，避免把令牌带进去）。
        pub fn reason(self) -> &'static str {
            match self {
                Denied::Scheme => "scheme",
                Denied::Host => "host",
                Denied::Port => "port",
                Denied::UserInfo => "userinfo",
                Denied::TokenInUrl => "token_in_url",
            }
        }
    }

    /// 敏感查询键：出现即拒绝（键名比较不区分大小写）。
    const SENSITIVE_KEYS: &[&str] = &[
        "token",
        "access_token",
        "id_token",
        "pat",
        "csrf",
        "idempotency_key",
        "api_key",
        "password",
        "secret",
    ];

    /// 判定一个 URL 是否允许导航。
    ///
    /// 允许的只有两处：应用自己的引导页（Tauri 内部协议）与本机 hub。
    /// 引导页必须放行，否则窗口一打开就被自己的白名单挡住；
    /// 它不是「外部导航」，而是这个壳自带的资源。
    pub fn allows(url: &Url) -> Result<(), Denied> {
        if is_app_internal(url) {
            return Ok(());
        }
        // 壳自己的协议只承载引导页：同协议下的别的 origin 是 host 不对，
        // 报 host 比笼统报 scheme 更接近原因（它既不是外部网页，也不是 https）。
        if url.scheme() == "tauri" {
            return Err(Denied::Host);
        }
        if url.scheme() != "http" {
            return Err(Denied::Scheme);
        }
        if !url.username().is_empty() || url.password().is_some() {
            return Err(Denied::UserInfo);
        }
        let host = match url.host_str() {
            Some(h) => h,
            None => return Err(Denied::Host),
        };
        let loopback = host == "localhost" || host == "::1" || host == "[::1]" || host.starts_with("127.");
        if !loopback {
            return Err(Denied::Host);
        }
        if url.port() != Some(HUB_PORT) {
            return Err(Denied::Port);
        }
        if token_in_url(url) {
            return Err(Denied::TokenInUrl);
        }
        Ok(())
    }

    /// 解析并判定；任一步失败都只是「不许跳」，不是崩溃。
    pub fn parse(raw: &str) -> Result<Url, Denied> {
        let url = Url::parse(raw.trim()).map_err(|_| Denied::Scheme)?;
        allows(&url)?;
        Ok(url)
    }

    /// hub 根地址，导航的默认目标。
    pub fn hub_root() -> Result<Url, Denied> {
        parse(&format!("{}/", super::HUB_ORIGIN))
    }

    /// 应用自带的引导页（`frontendDist="../ui"`）：macOS／Linux 走 `tauri://localhost`，
    /// Windows 走 `http(s)://tauri.localhost`。只认这两个 origin，别的一律不放行。
    fn is_app_internal(url: &Url) -> bool {
        match url.scheme() {
            "tauri" => url.host_str() == Some("localhost") || url.host_str().is_none(),
            "http" | "https" => url.host_str() == Some("tauri.localhost"),
            _ => false,
        }
    }

    fn token_in_url(url: &Url) -> bool {
        // 只看键名：值长什么样都可能是令牌，按契约「不得出现」处理。
        if let Some(q) = url.query() {
            for pair in q.split('&') {
                let key = pair.split('=').next().unwrap_or("").to_ascii_lowercase();
                if SENSITIVE_KEYS.contains(&key.as_str()) {
                    return true;
                }
                if pair.to_ascii_lowercase().contains("maw_pat_") {
                    return true;
                }
            }
        }
        if let Some(f) = url.fragment() {
            if f.to_ascii_lowercase().contains("maw_pat_") {
                return true;
            }
        }
        false
    }
}

/// 令牌脱敏：日志与错误文案只能出现「这里有个令牌」，不能出现令牌本身。
pub mod redact {
    /// 已知的令牌前缀：`maw_pat_...`（HTTP 契约的 CLI 令牌）与 `Bearer ` 头。
    const PREFIXES: &[&str] = &["maw_pat_", "Bearer ", "bearer "];

    /// 敏感查询键（与 `nav` 同一张表，脱敏时不区分大小写）。
    const SENSITIVE_KEYS: &[&str] = &[
        "token=",
        "access_token=",
        "id_token=",
        "pat=",
        "csrf=",
        "idempotency_key=",
        "api_key=",
        "password=",
        "secret=",
    ];

    /// 把一段外来文本里的令牌形状抹掉。
    pub fn scrub(input: &str) -> String {
        let masked = mask_after_prefix(input, PREFIXES);
        mask_query(&masked)
    }

    fn mask_after_prefix(input: &str, prefixes: &[&str]) -> String {
        let mut out = String::with_capacity(input.len());
        let mut rest = input;
        loop {
            let mut best: Option<(usize, &str)> = None;
            for p in prefixes {
                if let Some(i) = rest.find(p) {
                    if best.map_or(true, |(bi, _)| i < bi) {
                        best = Some((i, p));
                    }
                }
            }
            let Some((i, p)) = best else {
                out.push_str(rest);
                break;
            };
            out.push_str(&rest[..i + p.len()]);
            out.push_str("<redacted>");
            let after = &rest[i + p.len()..];
            let end = after
                .find(|c: char| !(c.is_ascii_alphanumeric() || c == '_' || c == '-' || c == '.'))
                .unwrap_or(after.len());
            rest = &after[end..];
        }
        out
    }

    fn mask_query(input: &str) -> String {
        input
            .split('&')
            .map(|seg| {
                let lower = seg.to_ascii_lowercase();
                if SENSITIVE_KEYS.iter().any(|k| lower.contains(k)) {
                    return match seg.split_once('=') {
                        Some((key, _)) => format!("{key}=<redacted>"),
                        None => "<redacted>".to_string(),
                    };
                }
                seg.to_string()
            })
            .collect::<Vec<_>>()
            .join("&")
    }
}

/// SSE 帧解析。
///
/// 只实现事件流用到的部分：`event:` 与 `data:`，块以空行分隔，`: keepalive` 注释丢弃，
/// `retry:` 之类的控制行忽略。解析器不猜、不重排、不合成帧。
pub mod frame {
    /// 一个完整的 SSE 帧。
    #[derive(Debug, Clone, PartialEq, Eq)]
    pub struct Frame {
        pub event: String,
        pub data: String,
    }

    /// 增量解析器：喂行，吐完整的帧。
    #[derive(Debug, Default)]
    pub struct Parser {
        buf: String,
    }

    impl Parser {
        /// 喂一行（不含换行）。返回本次凑齐的帧，可能为空。
        pub fn push_line(&mut self, line: &str) -> Vec<Frame> {
            self.buf.push_str(line);
            self.buf.push('\n');
            let mut out = Vec::new();
            while let Some(idx) = self.buf.find("\n\n") {
                let block: String = self.buf.drain(..idx + 2).collect();
                if let Some(f) = parse_block(&block) {
                    out.push(f);
                }
            }
            out
        }

        /// 连接断了：丢弃半截缓冲。半截帧不是事实，留着只会和新连接的字节拼错。
        pub fn reset(&mut self) {
            self.buf.clear();
        }
    }

    fn parse_block(block: &str) -> Option<Frame> {
        let mut event: Option<String> = None;
        let mut data = String::new();
        for line in block.lines() {
            if line.starts_with(':') {
                continue; // keepalive 注释
            }
            if let Some(v) = line.strip_prefix("event:") {
                event = Some(v.trim().to_string());
            } else if let Some(v) = line.strip_prefix("data:") {
                if !data.is_empty() {
                    data.push('\n');
                }
                data.push_str(v.strip_prefix(' ').unwrap_or(v));
            }
        }
        event.map(|e| Frame { event: e, data })
    }
}

/// 补拉游标：hub 的 `GET /api/feed?since=N` 与 SSE 的 `catchup` 帧共用。
pub mod feed {
    use serde_json::Value;

    /// 一次补拉页。
    #[derive(Debug, Clone, PartialEq, Eq)]
    pub struct Batch {
        /// 事件正文（原样 JSON，交给 `reminder` 解释）。
        pub events: Vec<Value>,
        /// 下次从哪里继续；`None` 表示这一页没给出可推进的位置，游标必须停住。
        pub next_since: Option<i64>,
        /// 是否还有积压。
        pub has_more: bool,
    }

    /// 解析 `/api/feed` 响应。
    ///
    /// 任何一处不合约就整体判空：**半份 feed 比没有 feed 更危险**——
    /// 按半份推进游标会永久跳过中间那条提醒。
    ///
    /// 兼容旧 hub：老版本只回 `{ok, events}`，既没有 `next_since` 也没有 `has_more`。
    /// 这时用本页最大事件身份当游标，并按「非空即可能还有」处理——多拉一页的代价
    /// 远小于漏掉一条提醒；下一页为空自然停下。
    pub fn parse_batch(body: &str) -> Option<Batch> {
        let v: Value = serde_json::from_str(body.trim()).ok()?;
        let events = v.get("events")?.as_array()?.clone();
        let next_since = v
            .get("next_since")
            .and_then(Value::as_i64)
            .or_else(|| events.iter().filter_map(event_id).max());
        let has_more = match v.get("has_more").and_then(Value::as_bool) {
            Some(explicit) => explicit,
            None => !events.is_empty(),
        };
        Some(Batch { events, next_since, has_more })
    }

    /// SSE `catchup` 帧给出的「从哪补」。
    ///
    /// 只用 `since`：`watermark` 是水位上限，跳过去就等于丢掉中间事件。
    pub fn catchup_since(body: &str) -> Option<i64> {
        let v: Value = serde_json::from_str(body.trim()).ok()?;
        v.get("since").and_then(Value::as_i64)
    }

    /// 单条事件的自增身份（`db.Event.ID`）。
    pub fn event_id(v: &Value) -> Option<i64> {
        match v.get("id") {
            Some(Value::Number(n)) => n.as_i64(),
            Some(Value::String(s)) => s.trim().parse::<i64>().ok(),
            _ => None,
        }
    }
}

/// 提醒分类：把一条 hub 事件翻成「要不要打断用户」。
pub mod reminder {
    use serde_json::Value;

    use super::feed::event_id;
    use super::frame::Frame;

    /// 需要用户介入的类型，与 `internal/notifications` 的 `Kind` 同名。
    #[derive(Debug, Clone, Copy, PartialEq, Eq)]
    pub enum Kind {
        /// 在等人：任务 blocked／conflict、运行 waiting。
        Waiting,
        /// 已经失败：门禁失败、运行 failed／timed_out。
        Failed,
        /// 交付有变化：待验收、任务完成、验收结论。
        Delivery,
    }

    impl Kind {
        pub fn as_str(self) -> &'static str {
            match self {
                Kind::Waiting => "waiting",
                Kind::Failed => "failed",
                Kind::Delivery => "delivery",
            }
        }

        /// 是否允许走系统通知。
        ///
        /// 与 `internal/notifications.DefaultPolicy` 对齐：交付变化只走站内。
        /// 这类事实频率高、单条价值低，弹窗会把人训练成「无脑关掉」。
        pub fn desktop_allowed(self) -> bool {
            !matches!(self, Kind::Delivery)
        }

        fn from_name(name: &str) -> Option<Kind> {
            match name {
                "waiting" => Some(Kind::Waiting),
                "failed" => Some(Kind::Failed),
                "delivery" => Some(Kind::Delivery),
                _ => None,
            }
        }

        /// 任务状态 → 提醒类型（与 `notifications.Classify` 同一张表）。
        fn from_task_status(status: &str) -> Option<Kind> {
            match status {
                "blocked" | "conflict" => Some(Kind::Waiting),
                "in_review" | "done" => Some(Kind::Delivery),
                _ => None,
            }
        }

        /// 运行阶段 → 提醒类型。
        fn from_run_phase(phase: &str) -> Option<Kind> {
            match phase {
                "waiting" => Some(Kind::Waiting),
                "failed" | "timed_out" => Some(Kind::Failed),
                _ => None,
            }
        }
    }

    /// 一条待投递的提醒事实。
    #[derive(Debug, Clone, PartialEq, Eq)]
    pub struct Fact {
        /// 事件身份：用于「同一条事件重放不重弹」。
        pub event_id: String,
        pub kind: Kind,
        /// 工程（参与指纹，避免跨工程同显示号互相压制）。
        pub project: String,
        /// 对象显示名：任务显示号或运行身份。
        pub subject: String,
        /// 停住的状态值。
        pub state: String,
    }

    impl Fact {
        /// 「同一对象停在同一状态」的判定键：类型 + 工程 + 对象 + 状态。
        ///
        /// 不含事件身份与时间，否则每次重放都是新指纹，去重形同虚设（与
        /// `internal/notifications/policy.go` 的 fingerprint 同一口径，多带工程）。
        pub fn fingerprint(&self) -> String {
            if self.project.is_empty() {
                format!("{}|{}|{}", self.kind.as_str(), self.subject, self.state)
            } else {
                format!(
                    "{}|{}|{}|{}",
                    self.kind.as_str(),
                    self.project,
                    self.subject,
                    self.state
                )
            }
        }
    }

    /// 把一帧翻成提醒事实。
    ///
    /// 只认两种帧：`event`（事件正文）与 `reminder`（若 hub 日后直接发提醒）。
    /// `ready`／`board-changed`／`catchup` 都不是事实来源，返回 `None`。
    pub fn classify_frame(frame: &Frame) -> Option<Fact> {
        match frame.event.as_str() {
            "event" | "reminder" => {
                let v: Value = serde_json::from_str(frame.data.trim()).ok()?;
                classify_value(&v)
            }
            _ => None,
        }
    }

    /// 把一条事件正文翻成提醒事实；表外一律 `None`（**不弹**）。
    ///
    /// 认两套命名：hub 事件账本的现有类型（`status_changed`／`gate_failed`），
    /// 以及契约里的领域事件名（`task.status`／`run.phase`／`delivery.review`）。
    /// 两套都只读已声明字段，不做跨类型猜键名。
    pub fn classify_value(v: &Value) -> Option<Fact> {
        if v.get("kind").is_some() && v.get("type").is_none() {
            return from_reminder_value(v);
        }
        let event_type = v.get("type")?.as_str()?;
        let data = v.get("data").unwrap_or(&Value::Null);

        let (kind, state) = match event_type {
            // 事件账本：任务状态迁移，载荷是 {from,to[,reason]}。
            "status_changed" => {
                let to = non_empty(data.get("to").and_then(Value::as_str))?;
                (Kind::from_task_status(&to)?, to)
            }
            // 事件账本：门禁失败。它就是要人处理的那类失败，与运行失败同级。
            "gate_failed" => (Kind::Failed, "gate_failed".to_string()),
            // 契约命名：任务状态事实。
            "task.status" => {
                let status = pick(v, data, "status")?;
                (Kind::from_task_status(&status)?, status)
            }
            // 契约命名：运行阶段事实。
            "run.phase" => {
                let phase = pick(v, data, "phase")?;
                (Kind::from_run_phase(&phase)?, phase)
            }
            // 契约命名：交付评审结论。
            "delivery.review" => {
                let verdict = pick(v, data, "verdict")?;
                match verdict.as_str() {
                    "approved" | "changes_required" => (Kind::Delivery, verdict),
                    _ => return None,
                }
            }
            _ => return None,
        };

        let project = non_empty(v.get("project_id").and_then(Value::as_str)).unwrap_or_default();
        let subject = pick(v, data, "task_id")
            .or_else(|| pick(v, data, "task_instance"))
            .or_else(|| pick(v, data, "display_id"))
            .or_else(|| pick(v, data, "run_id"))?;
        let event_id = match event_id(v) {
            Some(id) => id.to_string(),
            None => format!("{project}|{subject}|{event_type}|{state}"),
        };
        Some(Fact { event_id, kind, project, subject, state })
    }

    /// hub 若直接发提醒帧：字段已声明，按 `kind` 读。
    fn from_reminder_value(v: &Value) -> Option<Fact> {
        let kind = Kind::from_name(v.get("kind")?.as_str()?)?;
        let subject = non_empty(v.get("subject").and_then(Value::as_str))?;
        let project = non_empty(v.get("project").and_then(Value::as_str)).unwrap_or_default();
        let state = v.get("state").and_then(Value::as_str).unwrap_or("").to_string();
        let event_id = match v.get("event_id").or_else(|| v.get("id")) {
            Some(Value::String(s)) if !s.trim().is_empty() => s.clone(),
            Some(other) if !other.is_null() => other.to_string(),
            _ => format!("{project}|{subject}|{}|{state}", kind.as_str()),
        };
        Some(Fact { event_id, kind, project, subject, state })
    }

    fn pick(top: &Value, data: &Value, key: &str) -> Option<String> {
        non_empty(data.get(key).and_then(Value::as_str))
            .or_else(|| non_empty(top.get(key).and_then(Value::as_str)))
    }

    fn non_empty(v: Option<&str>) -> Option<String> {
        v.map(str::trim).filter(|s| !s.is_empty()).map(str::to_string)
    }
}

/// 投递闸门：权限、去重、状态未变、确认后的静默窗口。
///
/// 只记「弹过什么」和「这条被确认过」：
/// 确认事实由 hub 持有、只能经同步告知（客户端拿到后调 [`Gate::ack`]），桌面壳自己不推断——
/// 弹窗成功不等于用户看见了，更不能拿它当确认。
///
/// 于是判定只有四条，与 hub 侧 `notifications.Raise` 一一对应：
/// 同一事件同一渠道不重弹（`Duplicate`）、未确认的同对象同状态不再弹（`Unchanged`）、
/// 已确认的同对象同状态只在去重窗口内静默（`Settled`）、窗口外算新的介入需求。
/// 重启后最坏是重弹一次，这比「以为自己记得」安全得多。
pub mod gate {
    use std::collections::{HashMap, HashSet, VecDeque};

    use super::reminder::{Fact, Kind};

    /// 记账上限：只保留最近这么多条已投递事件，避免长跑进程无界增长。
    const MAX_TRACKED_EVENTS: usize = 512;

    /// 系统通知权限的最终结论（`Prompt` 由调用方先问一遍再交进来）。
    #[derive(Debug, Clone, Copy, PartialEq, Eq)]
    pub enum Permission {
        Granted,
        Denied,
    }

    /// 为什么没弹（或为什么只留站内）。
    #[derive(Debug, Clone, Copy, PartialEq, Eq)]
    pub enum Why {
        /// 交付类事实按默认策略只走站内。
        DeliveryInAppOnly,
        /// 系统通知权限没给：站内记录仍然在，不重复打扰。
        PermissionDenied,
        /// 同一条事件（事件身份 + 渠道）已经投过。
        Duplicate,
        /// 同一对象停在同一状态：没有变化就不打扰。
        Unchanged,
        /// 同一对象同一状态刚被确认过，仍在去重窗口内：用户处理过了。
        Settled,
    }

    /// 一次判定的结论。
    #[derive(Debug, Clone, PartialEq, Eq)]
    pub enum Decision {
        /// 弹系统通知。
        Notify,
        /// 只留站内提醒（不投桌面，也不回写）。
        InApp(Why),
        /// 什么也不做（重复／未变化／刚确认过）。
        Skip(Why),
    }

    /// 投递闸门。
    #[derive(Debug, Default)]
    pub struct Gate {
        delivered_events: HashSet<String>,
        delivered_order: VecDeque<String>,
        /// subject → 最近一次投递的指纹（`kind|project|subject|state`）。
        objects: HashMap<String, String>,
        /// subject → hub 告知的确认时间（毫秒）。没有条目就是「没确认过」。
        acked_at: HashMap<String, u64>,
    }

    impl Gate {
        /// 判定一条事实该怎么处理。
        ///
        /// `now_ms` 与 `window_ms` 显式传入而不是内部取时钟：判定必须是纯的，
        /// 否则「窗口内压、窗口外放」这类行为没法钉在测试里。
        pub fn decide(
            &mut self,
            fact: &Fact,
            permission: Permission,
            now_ms: u64,
            window_ms: u64,
        ) -> Decision {
            if !fact.kind.desktop_allowed() {
                return Decision::InApp(Why::DeliveryInAppOnly);
            }
            if permission != Permission::Granted {
                // 被拒时不记账：拿到权限之后同一条事实还要能弹。
                return Decision::InApp(Why::PermissionDenied);
            }
            // 渠道固定是 desktop：站内那条记录不归桌面管，也不需要桌面去投。
            let key = format!("{}|desktop", fact.event_id);
            if self.delivered_events.contains(&key) {
                return Decision::Skip(Why::Duplicate);
            }
            let fingerprint = fact.fingerprint();
            let object_key = object_key(fact);
            if self
                .objects
                .get(&object_key)
                .is_some_and(|prev| *prev == fingerprint)
            {
                // 状态没变：未确认的无条件压制；确认过的只在窗口内压制。
                // 窗口按 hub 的口径算「确认时间 ≥ now − window」为窗口内，
                // 时钟回拨（`now_ms < acked_at`）也按窗口内处理，宁可不打扰。
                return match self.acked_at.get(&object_key).copied() {
                    Some(at) if now_ms.saturating_sub(at) <= window_ms => {
                        Decision::Skip(Why::Settled)
                    }
                    Some(_) => {
                        self.remember(key, object_key, fingerprint);
                        Decision::Notify
                    }
                    None => Decision::Skip(Why::Unchanged),
                };
            }

            self.remember(key, object_key, fingerprint);
            Decision::Notify
        }

        /// 记下确认事实（`subject` 被确认，时间 `at_ms`）。
        ///
        /// 只影响「同对象同状态还要不要沉默」，不改变投递记账：
        /// 确认是业务状态，不是「弹过了」。
        ///
        /// 确认由 hub 的 `notification_deliveries` 持有，桌面壳只能被告知、不能自己推断。
        /// 事件流目前还没有确认帧，所以这里只留入口、并用单测钉住窗口语义。
        #[allow(dead_code)]
        pub fn ack(&mut self, project: &str, subject: &str, at_ms: u64) {
            self.acked_at.insert(pair_key(project, subject), at_ms);
        }

        /// 投递失败：撤销这次记账。
        ///
        /// 桌面投递不是原子的（弹窗可能被系统拒绝、用户可能关掉权限），
        /// 失败后必须让它有机会重试；否则「弹失败」会被记成「弹过了」，人就永远收不到。
        pub fn forget(&mut self, fact: &Fact) {
            let key = format!("{}|desktop", fact.event_id);
            self.delivered_events.remove(&key);
            self.delivered_order.retain(|k| *k != key);
            self.objects.remove(&object_key(fact));
        }

        fn remember(&mut self, key: String, object_key: String, fingerprint: String) {
            // 新一轮提醒开始：上一次的确认不再对这次生效，否则刚放行的提醒会被旧确认压住。
            self.acked_at.remove(&object_key);
            self.objects.insert(object_key, fingerprint);
            if self.delivered_events.insert(key.clone()) {
                self.delivered_order.push_back(key);
            }
            while self.delivered_order.len() > MAX_TRACKED_EVENTS {
                if let Some(old) = self.delivered_order.pop_front() {
                    self.delivered_events.remove(&old);
                }
            }
        }
    }

    fn pair_key(project: &str, subject: &str) -> String {
        if project.is_empty() {
            subject.to_string()
        } else {
            format!("{project}|{subject}")
        }
    }

    fn object_key(fact: &Fact) -> String {
        pair_key(&fact.project, &fact.subject)
    }

    /// 通知标题与类型词（中英各一份，与托盘菜单同一口径）。
    pub fn labels(kind: Kind, zh: bool) -> (&'static str, &'static str) {
        match (zh, kind) {
            (true, Kind::Waiting) => ("maw 需要你介入", "在等你处理"),
            (true, Kind::Failed) => ("maw 有运行失败", "失败待处理"),
            (true, Kind::Delivery) => ("maw 交付有变化", "待验收"),
            (false, Kind::Waiting) => ("maw needs you", "waiting on you"),
            (false, Kind::Failed) => ("maw run failed", "failed"),
            (false, Kind::Delivery) => ("maw delivery changed", "review needed"),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::feed::{catchup_since, parse_batch};
    use super::gate::{Decision, Gate, Permission, Why};
    use super::nav::{allows, parse, Denied};
    use super::redact::scrub;
    use super::reminder::{classify_frame, classify_value, Fact, Kind};
    use super::{frame::Parser, HUB_ORIGIN};
    use tauri::Url;

    fn frame(event: &str, data: &str) -> super::frame::Frame {
        super::frame::Frame {
            event: event.to_string(),
            data: data.to_string(),
        }
    }

    // ---- 导航白名单 ----

    #[test]
    fn nav_allows_only_hub_origin() {
        assert_eq!(
            parse(HUB_ORIGIN).map(|u| u.as_str().to_string()),
            Ok(format!("{HUB_ORIGIN}/"))
        );
        assert!(allows(&Url::parse(&format!("{HUB_ORIGIN}/?view=board")).unwrap()).is_ok());
        assert!(allows(&Url::parse(&format!("{HUB_ORIGIN}/#task=VN-902")).unwrap()).is_ok());

        for (raw, want) in [
            ("https://127.0.0.1:7420/", Denied::Scheme),
            ("http://example.com:7420/", Denied::Host),
            ("http://127.0.0.1:7421/", Denied::Port),
            ("http://user:pw@127.0.0.1:7420/", Denied::UserInfo),
            ("http://127.0.0.1:7420/?token=abc", Denied::TokenInUrl),
            ("http://127.0.0.1:7420/?access_token=abc", Denied::TokenInUrl),
            ("http://127.0.0.1:7420/?idempotency_key=k", Denied::TokenInUrl),
            ("http://127.0.0.1:7420/?q=maw_pat_abc", Denied::TokenInUrl),
        ] {
            assert_eq!(parse(raw).err(), Some(want), "should deny {raw}");
        }
    }

    #[test]
    fn nav_allows_app_bootstrap() {
        // 引导页是壳自带资源，不能被白名单挡住。
        assert!(parse("tauri://localhost/index.html").is_ok());
        assert!(parse("http://tauri.localhost/index.html").is_ok());
        // 别的 tauri 协议 origin 不放行。
        assert_eq!(parse("tauri://evil/index.html").err(), Some(Denied::Host));
    }

    // ---- 令牌脱敏 ----

    #[test]
    fn redact_masks_tokens_and_query_values() {
        assert_eq!(scrub("Bearer maw_pat_abc123"), "Bearer <redacted>");
        assert_eq!(scrub("token=maw_pat_abc123"), "token=<redacted>");
        assert_eq!(
            scrub("http://127.0.0.1:7420/?token=abc&view=board"),
            "http://127.0.0.1:7420/?token=<redacted>&view=board"
        );
        assert_eq!(scrub("plain text"), "plain text");
        assert!(!scrub("Authorization: Bearer maw_pat_secret").contains("secret"));
    }

    // ---- SSE 解析 ----

    #[test]
    fn frame_parser_handles_split_and_keepalive() {
        let mut p = Parser::default();
        assert!(p.push_line(": keepalive").is_empty());
        assert!(p.push_line("retry: 3000").is_empty());
        assert!(p.push_line("event: board-changed").is_empty());
        let f = p.push_line("data: {}");
        assert!(f.is_empty());
        let f = p.push_line("");
        assert_eq!(f.len(), 1);
        assert_eq!(f[0].event, "board-changed");
        assert_eq!(f[0].data, "{}");

        // 半截帧在断线后被丢弃，不和新连接的字节拼起来。
        let _ = p.push_line("event: event");
        p.reset();
        assert_eq!(p.push_line("data: {}").len(), 0);
    }

    #[test]
    fn frame_parser_keeps_event_payload() {
        let mut p = Parser::default();
        let line = r#"data: {"id":7,"type":"status_changed","task_id":"VN-902","data":{"to":"blocked"}}"#;
        assert!(p.push_line("event: event").is_empty());
        assert!(p.push_line(line).is_empty(), "空行之前不算一帧");
        let frames = p.push_line("");
        assert_eq!(frames.len(), 1);
        assert_eq!(frames[0].event, "event");
        assert!(frames[0].data.contains("status_changed"));
    }

    // ---- 分类：只认已声明的事实 ----

    #[test]
    fn classify_task_status_from_ledger_events() {
        // 真实账本形状：type=status_changed，载荷 {from,to}。
        let blocked = classify_value(&serde_json::json!({
            "id": 42,
            "project_id": "p1",
            "actor": "maw",
            "type": "status_changed",
            "task_id": "VN-902",
            "data": {"from": "in_progress", "to": "blocked", "reason": "gate"}
        }))
        .expect("blocked 应被识别");
        assert_eq!(blocked.kind, Kind::Waiting);
        assert_eq!(blocked.subject, "VN-902");
        assert_eq!(blocked.state, "blocked");
        assert_eq!(blocked.event_id, "42");
        assert_eq!(blocked.fingerprint(), "waiting|p1|VN-902|blocked");

        let review = classify_value(&serde_json::json!({
            "id": 43, "project_id": "p1", "type": "status_changed",
            "task_id": "VN-902", "data": {"from": "submitted", "to": "in_review"}
        }))
        .expect("in_review 应被识别");
        assert_eq!(review.kind, Kind::Delivery);

        // 表外状态：不弹（claimed/in_progress/ready/submitted/merging…）。
        for to in ["claimed", "in_progress", "ready", "submitted", "merging", "unknown"] {
            let v = serde_json::json!({
                "id": 1, "type": "status_changed", "task_id": "VN-902", "data": {"to": to}
            });
            assert!(classify_value(&v).is_none(), "should ignore status {to}");
        }
    }

    #[test]
    fn classify_gate_failure_as_failed() {
        let f = classify_value(&serde_json::json!({
            "id": 51, "project_id": "p1", "type": "gate_failed",
            "task_id": "VN-606", "data": {"env_failure": false, "error": "go test ./..."}
        }))
        .expect("gate_failed 应被识别");
        assert_eq!(f.kind, Kind::Failed);
        assert_eq!(f.subject, "VN-606");
        assert_eq!(f.state, "gate_failed");
    }

    #[test]
    fn classify_declared_domain_events() {
        // 契约命名的领域事件（v1 摄取器写的那套）。
        let phase = classify_value(&serde_json::json!({
            "id": "evt_1", "project_id": "p1", "type": "run.phase",
            "data": {"phase": "waiting", "run_id": "run_1"}
        }))
        .expect("run.phase=waiting 应被识别");
        assert_eq!(phase.kind, Kind::Waiting);
        assert_eq!(phase.subject, "run_1");

        for (phase, want) in [("failed", Kind::Failed), ("timed_out", Kind::Failed)] {
            let f = classify_value(&serde_json::json!({
                "id": 2, "type": "run.phase", "data": {"phase": phase, "run_id": "run_2"}
            }))
            .expect("失败阶段应被识别");
            assert_eq!(f.kind, want);
        }

        let verdict = classify_value(&serde_json::json!({
            "id": 3, "type": "delivery.review",
            "data": {"verdict": "changes_required", "task_id": "VN-902"}
        }))
        .expect("评审结论应被识别");
        assert_eq!(verdict.kind, Kind::Delivery);

        // 表外：运行正常结束、未知结论、缺对象。
        for v in [
            serde_json::json!({"id": 4, "type": "run.phase", "data": {"phase": "running", "run_id": "r"}}),
            serde_json::json!({"id": 5, "type": "delivery.review", "data": {"verdict": "not_required", "task_id": "T"}}),
            serde_json::json!({"id": 6, "type": "run.phase", "data": {"phase": "waiting"}}),
            serde_json::json!({"id": 7, "type": "lease.acquired", "data": {}}),
            serde_json::json!({"id": 8, "type": "progress", "task_id": "VN-902", "data": {"note": "x"}}),
        ] {
            assert!(classify_value(&v).is_none(), "should ignore {v}");
        }
    }

    #[test]
    fn classify_frame_ignores_non_fact_frames() {
        // SSE 的 ready／board-changed／catchup 只是失效提示，不是事实。
        assert!(classify_frame(&frame("ready", "{}")).is_none());
        assert!(classify_frame(&frame("board-changed", "{}")).is_none());
        assert!(classify_frame(&frame("catchup", r#"{"since":1,"watermark":9}"#)).is_none());
        assert!(classify_frame(&frame("event", "not-json")).is_none());
        assert!(classify_frame(&frame("event", r#"{"id":1,"type":"progress"}"#)).is_none());

        let f = classify_frame(&frame(
            "event",
            r#"{"id":9,"type":"status_changed","task_id":"VN-1","data":{"to":"conflict"}}"#,
        ))
        .expect("event 帧应被解释");
        assert_eq!(f.kind, Kind::Waiting);
    }

    #[test]
    fn classify_accepts_declared_reminder_frame() {
        let f = classify_value(&serde_json::json!({
            "event_id": "evt_1", "kind": "failed", "project": "p1",
            "subject": "VN-902", "state": "failed"
        }))
        .expect("已声明的提醒帧应被接受");
        assert_eq!(f.kind, Kind::Failed);
        assert_eq!(f.fingerprint(), "failed|p1|VN-902|failed");
    }

    // ---- feed 补拉 ----

    #[test]
    fn feed_batch_advances_only_by_events() {
        let body = r#"{"ok":true,"events":[
            {"id":11,"type":"status_changed","task_id":"VN-1","data":{"to":"blocked"}},
            {"id":12,"type":"status_changed","task_id":"VN-2","data":{"to":"blocked"}}],
            "has_more":true,"next_since":12}"#;
        let b = parse_batch(body).expect("应能解析");
        assert_eq!(b.events.len(), 2);
        assert_eq!(b.next_since, Some(12));
        assert!(b.has_more);

        // 空页也要能推进水位，否则积压永远追不上。
        let empty = parse_batch(r#"{"ok":true,"events":[],"has_more":false,"next_since":12}"#)
            .expect("空页也应可解析");
        assert_eq!(empty.next_since, Some(12));
        assert!(!empty.has_more);

        // 坏响应整体判空：宁可不动游标，也不能按半份数据跳事件。
        assert!(parse_batch("not-json").is_none());
        assert!(parse_batch(r#"{"ok":true}"#).is_none());
    }

    #[test]
    fn feed_batch_accepts_old_hub_shape() {
        // 旧 hub 只回 {ok, events}：游标退化为本页最大身份，并按「可能还有」继续。
        let b = parse_batch(
            r#"{"ok":true,"events":[{"id":7,"type":"progress"},{"id":9,"type":"progress"}]}"#,
        )
        .expect("旧形状也应可解析");
        assert_eq!(b.next_since, Some(9));
        assert!(b.has_more, "非空页按可能还有处理，多拉一页换不漏事件");

        let tail = parse_batch(r#"{"ok":true,"events":[]}"#).expect("空页可解析");
        assert_eq!(tail.next_since, None, "空页不提供可推进位置");
        assert!(!tail.has_more);
    }

    #[test]
    fn catchup_uses_since_not_watermark() {
        assert_eq!(catchup_since(r#"{"since":7,"watermark":900}"#), Some(7));
        assert_eq!(catchup_since(r#"{"watermark":900}"#), None);
    }

    // ---- 投递闸门 ----

    fn waiting(id: &str, subject: &str, state: &str) -> Fact {
        Fact {
            event_id: id.to_string(),
            kind: Kind::Waiting,
            project: "p1".to_string(),
            subject: subject.to_string(),
            state: state.to_string(),
        }
    }

    #[test]
    fn gate_dedupes_by_event_and_channel() {
        let mut g = Gate::default();
        let f = waiting("1", "run_1", "waiting");
        assert_eq!(g.decide(&f, Permission::Granted, 0, 1_000), Decision::Notify);
        // 同一条事件重放：不重复弹。
        assert_eq!(
            g.decide(&f, Permission::Granted, 10, 1_000),
            Decision::Skip(Why::Duplicate)
        );
        // 新事件但同一对象同一状态：也不弹。
        let again = waiting("2", "run_1", "waiting");
        assert_eq!(
            g.decide(&again, Permission::Granted, 20, 1_000),
            Decision::Skip(Why::Unchanged)
        );
        // 状态变了：允许再提醒。
        let changed = waiting("3", "run_1", "blocked");
        assert_eq!(g.decide(&changed, Permission::Granted, 30, 1_000), Decision::Notify);
    }

    #[test]
    fn gate_keeps_in_app_when_permission_denied() {
        let mut g = Gate::default();
        let f = waiting("1", "run_1", "waiting");
        assert_eq!(
            g.decide(&f, Permission::Denied, 0, 1_000),
            Decision::InApp(Why::PermissionDenied)
        );
        // 被拒时不占位：拿到权限之后同一条事实还能弹。
        assert_eq!(g.decide(&f, Permission::Granted, 10, 1_000), Decision::Notify);
    }

    #[test]
    fn gate_keeps_delivery_to_in_app() {
        let mut g = Gate::default();
        let f = Fact {
            event_id: "9".into(),
            kind: Kind::Delivery,
            project: "p1".into(),
            subject: "VN-902".into(),
            state: "in_review".into(),
        };
        assert_eq!(
            g.decide(&f, Permission::Granted, 0, 1_000),
            Decision::InApp(Why::DeliveryInAppOnly)
        );
    }

    #[test]
    fn gate_renotifies_only_after_ack_and_window() {
        let mut g = Gate::default();
        let f = waiting("1", "run_1", "waiting");
        assert_eq!(g.decide(&f, Permission::Granted, 0, 1_000), Decision::Notify);
        g.ack("p1", "run_1", 100);
        // 窗口内：压制。
        let f2 = waiting("2", "run_1", "waiting");
        assert_eq!(
            g.decide(&f2, Permission::Granted, 200, 1_000),
            Decision::Skip(Why::Settled)
        );
        // 窗口外：允许再提醒。
        let f3 = waiting("3", "run_1", "waiting");
        assert_eq!(g.decide(&f3, Permission::Granted, 5_000, 1_000), Decision::Notify);
        // 放行之后旧确认不再残留：否则下一次「状态未变」会被过期的 acks 压错。
        let f4 = waiting("4", "run_1", "waiting");
        assert_eq!(
            g.decide(&f4, Permission::Granted, 5_010, 1_000),
            Decision::Skip(Why::Unchanged)
        );
    }

    #[test]
    fn gate_scopes_objects_per_project() {
        // 两个工程各有同显示号的任务：互不压制。
        let mut g = Gate::default();
        let a = Fact {
            event_id: "1".into(),
            kind: Kind::Waiting,
            project: "p1".into(),
            subject: "VN-902".into(),
            state: "blocked".into(),
        };
        let b = Fact {
            event_id: "2".into(),
            kind: Kind::Waiting,
            project: "p2".into(),
            subject: "VN-902".into(),
            state: "blocked".into(),
        };
        assert_eq!(g.decide(&a, Permission::Granted, 0, 1_000), Decision::Notify);
        assert_eq!(g.decide(&b, Permission::Granted, 10, 1_000), Decision::Notify);
    }

    #[test]
    fn gate_retries_after_delivery_failure() {
        let mut g = Gate::default();
        let f = waiting("1", "run_1", "waiting");
        assert_eq!(g.decide(&f, Permission::Granted, 0, 1_000), Decision::Notify);
        g.forget(&f);
        // 投递失败后同一条事实还能再试一次。
        assert_eq!(g.decide(&f, Permission::Granted, 10, 1_000), Decision::Notify);
    }

    #[test]
    fn gate_labels_follow_language() {
        assert_eq!(super::gate::labels(Kind::Waiting, true).0, "maw 需要你介入");
        assert_eq!(super::gate::labels(Kind::Waiting, false).0, "maw needs you");
    }
}
