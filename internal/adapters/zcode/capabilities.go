package zcode

import "github.com/kiwh77/maw/internal/engine/model"

const (
	capRuntimeProbe   = "entry.runtime_probe"
	capProviderConfig = "config.provider_explicit"
	capModelReady     = "config.model_ready"
	capManagedLaunch  = "managed.launch"
	capModelInvoke    = "model.invoke"
	capCancel         = "cancel.graceful_stop"
	capResume         = "session.resume"
)

func capabilitySet(version model.SemVer, runtimeOK, providerOK, modelOK bool) []model.Capability {
	ver := version
	if ver == "" {
		ver = "0.0.0"
	}
	type row struct {
		id, name string
		state    model.CapabilityState
	}
	rows := []row{
		{"Z01", capRuntimeProbe, pick(runtimeOK, model.CapabilityObserved, model.CapabilityUnobserved)},
		{"Z02", capProviderConfig, pick(providerOK, model.CapabilityObserved, model.CapabilityUnobserved)},
		{"Z03", capModelReady, pick(modelOK, model.CapabilityObserved, model.CapabilityUnobserved)},
		{"Z04", capManagedLaunch, model.CapabilityUnobserved},
		{"Z05", capModelInvoke, model.CapabilityUnobserved},
		{"Z06", capCancel, model.CapabilityUnknown},
		{"Z07", capResume, model.CapabilityUnknown},
	}
	// 仅当三列都就绪且版本命中时开放受管启动；模型调用仍保持 unobserved（本卡未实测）
	if runtimeOK && providerOK && modelOK && version == MeasuredCLIVersion {
		rows[3].state = model.CapabilityObserved // managed.launch
	}
	out := make([]model.Capability, 0, len(rows))
	for _, r := range rows {
		out = append(out, model.Capability{
			Name: r.name, State: r.state, Version: ver,
			EvidenceRef: EvidenceRoot + "/capability-matrix.json#" + r.id,
		})
	}
	return out
}

func pick(ok bool, yes, no model.CapabilityState) model.CapabilityState {
	if ok {
		return yes
	}
	return no
}

func capIndex(caps []model.Capability) map[string]model.CapabilityState {
	m := map[string]model.CapabilityState{}
	for _, c := range caps {
		m[c.Name] = c.State
	}
	return m
}
