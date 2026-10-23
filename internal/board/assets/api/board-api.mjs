// `/api/v1` 资源客户端（vnext / UI）。
//
// 只做「路径 + 载荷」到请求上下文的映射，不缓存业务结论、不算统计。
// 契约：docs/contracts/vnext/http/http-api-contract-v1.md（maw.http.api/v1）§1 资源与动作。
//
// 分工：
//   read  —— 纯读，冲突即报错，不自动重试；
//   write —— 同步幂等小写入（status-reports），必须显式声明不带 revision；
//   action—— 异步动作，202 之后轮询到终态才算成功。

/** 显示编号（VN-005）不得进路径，只能走 ?display_id=（契约 §1 命名规则）。 */
const INSTANCE_ID_PATTERN = /^ti_[A-Za-z0-9._:-]+$/;

function isInstanceId(value) {
  return INSTANCE_ID_PATTERN.test(String(value || ""));
}

function taskPath(taskRef, query = {}) {
  if (isInstanceId(taskRef)) {
    return { path: "/api/v1/tasks/" + encodeURIComponent(taskRef), query };
  }
  return {
    path: "/api/v1/tasks/" + encodeURIComponent(taskRef),
    query: { ...query, display_id: String(taskRef) },
  };
}

export function createBoardApi(context) {
  if (!context) throw new Error("createBoardApi: 需要 request-context 实例");

  const projectBase = pid => "/api/v1/projects/" + encodeURIComponent(pid);

  return {
    /** 看板快照（工程／状态／Agent 过滤）。 */
    getBoard(filters = {}, opts = {}) {
      return context.read("/api/v1/board", {
        ...opts,
        query: { project: filters.project, status: filters.status, agent: filters.agent },
      });
    },

    listProjects(opts = {}) {
      return context.read("/api/v1/projects", opts);
    },

    /** 任务列表：游标分页，limit 由服务端校验 1–200（客户端不静默截断）。 */
    listTasks(projectId, { limit, cursor } = {}, opts = {}) {
      return context.read(projectBase(projectId) + "/tasks", { ...opts, query: { limit, cursor } });
    },

    getTask(taskRef, opts = {}) {
      const { path, query } = taskPath(taskRef);
      return context.read(path, { ...opts, query });
    },

    listAgents(projectId, opts = {}) {
      return context.read(projectBase(projectId) + "/agents", opts);
    },

    /** 事件按 cursor 增量拉取，不允许「按时间重查全量」。 */
    listEvents({ cursor, limit } = {}, opts = {}) {
      return context.read("/api/v1/events", { ...opts, query: { cursor, limit } });
    },

    /** 领取（工程级异步动作）。 */
    claim(projectId, payload = {}, opts = {}) {
      return context.action(projectBase(projectId) + "/claims", {
        ...opts,
        actionKey: opts.actionKey || "claim:" + projectId + ":" + (payload.task_id || "next"),
        body: payload,
      });
    },

    /** 状态迁移（block／cancel 等），异步。 */
    transitionTask(taskRef, payload = {}, opts = {}) {
      const { path, query } = taskPath(taskRef);
      return context.action(path + "/status-transitions", {
        ...opts,
        query,
        actionKey: opts.actionKey || "transition:" + taskRef + ":" + (payload.to || ""),
        body: payload,
        revisionPath: path,
      });
    },

    submitTask(taskRef, payload = {}, opts = {}) {
      const { path, query } = taskPath(taskRef);
      return context.action(path + "/submissions", {
        ...opts,
        query,
        actionKey: opts.actionKey || "submit:" + taskRef,
        body: payload,
        revisionPath: path,
      });
    },

    mergeTask(taskRef, payload = {}, opts = {}) {
      const { path, query } = taskPath(taskRef);
      return context.action(path + "/merges", {
        ...opts,
        query,
        actionKey: opts.actionKey || "merge:" + taskRef,
        body: payload,
        revisionPath: path,
      });
    },

    /**
     * 来源状态上报：同步、幂等、不带 revision。
     * 契约 §7.4：只允许 reported_status ∈ {idle, working, blocked, offline}，
     * 不得借上报改任务业务状态——响应里的 changed_task_status 必须是 false。
     */
    async reportStatus(payload, opts = {}) {
      const result = await context.write("POST", "/api/v1/status-reports", {
        ...opts,
        revisionFree: true,
        actionKey: opts.actionKey || "status-report:" + (payload.actor || "") + ":" + (payload.task_id || ""),
        body: payload,
      });
      if (result.stale) return result;
      const changed = result.data?.changed_task_status;
      if (changed !== false) {
        throw new Error("status-report 不得改任务状态：服务端未返回 changed_task_status:false");
      }
      return result;
    },

    getOperation(operationId, opts = {}) {
      return context.read("/api/v1/operations/" + encodeURIComponent(operationId), opts);
    },
  };
}
