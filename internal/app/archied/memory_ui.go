package archied

import (
	"github.com/samcharles93/archie-core/internal/memory"
	"github.com/samcharles93/archie-core/internal/webui"
)

// memoryUIAdapter narrows the running memory runtime to the webui-owned
// MemoryStatus view. The webui deliberately does not link the memory
// package (archie-core-8cda.5.6), so the process that owns the runtime
// supplies this adapter at bootstrap. It renders names, readiness, and the
// tool vocabulary by name; it never hands the UI a live tool.
type memoryUIAdapter struct{ m *memory.Manager }

var _ webui.MemoryStatus = memoryUIAdapter{}

func (a memoryUIAdapter) Builtin() webui.MemoryProviderHandle {
	return memoryProviderUIAdapter{a.m.Builtin()}
}

func (a memoryUIAdapter) External() webui.MemoryProviderHandle {
	return memoryProviderUIAdapter{a.m.External()}
}

// memoryProviderUIAdapter converts one provider's runtime tool entries into
// the webui's name/description view. A nil provider renders as absent.
type memoryProviderUIAdapter struct{ p memory.MemoryProvider }

func (a memoryProviderUIAdapter) Name() string      { return a.p.Name() }
func (a memoryProviderUIAdapter) IsAvailable() bool { return a.p.IsAvailable() }
func (a memoryProviderUIAdapter) ToolViews() []webui.MemoryToolView {
	if a.p == nil {
		return nil
	}
	schemas := a.p.GetToolSchemas()
	views := make([]webui.MemoryToolView, 0, len(schemas))
	for _, t := range schemas {
		views = append(views, webui.MemoryToolView{Name: t.Name, Description: t.Description})
	}
	return views
}
