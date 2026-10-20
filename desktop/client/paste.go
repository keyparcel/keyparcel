package main

/*
#include "ui.h"
*/
import "C"

// Native fixture helpers use the same clipboard action and GtkTextBuffer as the UI.
func (m *model) pasteFixture(text string, start, end int, clip string) {
	cstrings([]string{text, clip}, func(v []*C.char) {
		C.ui_draft_select(m.ui, v[0], C.int(start), C.int(end))
		C.ui_copy(m.ui, v[1])
	})
}
func (m *model) activatePaste()   { C.ui_activate_paste(m.ui) }
func (m *model) draftCursor() int { return int(C.ui_draft_cursor(m.ui)) }
func (m *model) nativeDraft() string {
	text := C.ui_draft(m.ui)
	defer C.g_free(C.gpointer(text))
	return C.GoString(text)
}
func (m *model) setNativeBusy(busy bool)    { C.ui_busy(m.ui, boolean(busy)) }
func (m *model) setPasteLocked(locked bool) { C.ui_send_state(m.ui, 0, boolean(locked), 0, 0, 1) }
func (m *model) clearResults()              { C.ui_clear_results(m.ui) }
func (m *model) pasteState() informationState {
	n := C.ui_paste_state(m.ui)
	return informationState{x: int(n.x), y: int(n.y), width: int(n.width), height: int(n.height), mapped: n.mapped != 0, focused: n.focused != 0}
}
