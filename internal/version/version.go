package version

// Version is overwritten at release build via:
//
//	-ldflags "-X github.com/kiwh77/maw/internal/version.Version=x.y.z"
var Version = "0.4.0"

const (
	ProtocolVersion = 7
	SchemaVersion   = 3
)
