package version

// Version is overwritten at release build via:
//
//	-ldflags "-X github.com/kiwh77/maw/internal/version.Version=x.y.z"
var Version = "0.4.1"

const (
	ProtocolVersion = 9
	// SchemaVersion 跟随迁移台账末端（见 internal/db.LatestVersion）。
	SchemaVersion = 7
)
