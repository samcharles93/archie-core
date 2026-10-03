package servicekit

import (
	"github.com/samcharles93/archie-core/internal/buildinfo"
	"github.com/samcharles93/archie-core/internal/domain/presence"
	"github.com/samcharles93/archie-core/internal/installtype"
)

// Build is what this binary was compiled as, for its presence record.
func Build() presence.Build {
	return presence.Build{Version: buildinfo.Version, InstallType: installtype.Type()}
}
