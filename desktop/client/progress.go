package main

/*
#include "ui.h"
*/
import "C"

import (
	"context"
	"fmt"
	"runtime/cgo"

	"keyparcel/core"
)

func (m *model) queueProgress(ctx context.Context, sequence, version uint64, p core.Progress) {
	callback := cgo.NewHandle(func() {
		if m.closed || m.sequence != sequence || m.profileVersion != version || m.active == nil || ctx.Err() != nil {
			return
		}
		text := p.Phase
		fraction := -1.0
		if p.Total > 0 {
			fraction = min(1.0, float64(p.Bytes)/float64(p.Total))
			text = fmt.Sprintf("%s %d%%", p.Phase, int(fraction*100))
			if p.Items > 1 {
				text = fmt.Sprintf("%s %d/%d · %d%%", p.Phase, p.Item, p.Items, int(fraction*100))
			}
		}
		cstrings([]string{text}, func(v []*C.char) { C.ui_transfer_progress(m.ui, v[0], C.double(fraction)) })
	})
	C.ui_ref(m.ui)
	C.ui_post(m.ui, C.uintptr_t(callback))
}
func (m *model) transferFraction() float64 { return float64(C.ui_transfer_fraction(m.ui)) }
