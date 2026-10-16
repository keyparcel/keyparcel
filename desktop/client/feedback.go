package main

/*
#include "ui.h"
*/
import "C"

import (
	"fmt"
	"time"

	"keyparcel/core"
)

const feedbackDuration = 5 * time.Second

type sendFeedback struct {
	Deliveries        []core.Delivery
	Expanded, Visible bool
	DismissAt         time.Time
}

func newSendFeedback(rows []core.Delivery, now time.Time) *sendFeedback {
	f := &sendFeedback{Deliveries: append([]core.Delivery(nil), rows...), Visible: true}
	f.Expanded = !f.complete()
	if !f.Expanded {
		f.DismissAt = now.Add(feedbackDuration)
	}
	return f
}
func (f *sendFeedback) complete() bool {
	if len(f.Deliveries) == 0 {
		return false
	}
	for _, d := range f.Deliveries {
		if d.Error != "" || d.Unconfirmed {
			return false
		}
	}
	return true
}
func deliveryOutcome(d core.Delivery) string {
	if d.Unconfirmed {
		return "unconfirmed — check the inbox before sending again"
	}
	if d.Error != "" {
		return "failed — " + d.Error
	}
	return "delivered"
}
func (f *sendFeedback) headline() string {
	if !f.complete() {
		return "Send incomplete"
	}
	title := fmt.Sprintf("Sent to %d devices", len(f.Deliveries))
	if len(f.Deliveries) == 1 {
		title = "Sent to " + recipientLabel(f.Deliveries[0].Recipient)
	}
	id := f.Deliveries[0].ID
	for _, d := range f.Deliveries {
		if d.ID != id {
			id = ""
			break
		}
	}
	if id != "" {
		title += " · " + id[:min(8, len(id))]
	}
	return title
}
func (f *sendFeedback) expand(expanded bool, now time.Time) {
	if f.Expanded == expanded {
		return
	}
	f.Expanded = expanded
	f.DismissAt = time.Time{}
	if f.complete() && !expanded {
		f.DismissAt = now.Add(feedbackDuration)
	}
}
func (f *sendFeedback) expire(now time.Time) bool {
	if !f.Visible || f.Expanded || !f.complete() || f.DismissAt.IsZero() || now.Before(f.DismissAt) {
		return false
	}
	f.Visible = false
	return true
}
func (m *model) showSendFeedback(rows []core.Delivery) {
	open := m.feedback != nil && m.feedback.Expanded
	m.feedback = newSendFeedback(rows, time.Now())
	if open {
		m.feedback.expand(true, time.Now())
	}
	m.drawFeedback()
}
func (m *model) clearSendFeedback() { m.feedback = nil; m.drawFeedback() }
func (m *model) drawFeedback() {
	f := m.feedback
	if f == nil {
		cstrings([]string{""}, func(v []*C.char) { C.ui_feedback(m.ui, v[0], 0, 0) })
		return
	}
	m.clear("feedback-details")
	for _, d := range f.Deliveries {
		cstrings([]string{visible(recipientLabel(d.Recipient)), visible(deliveryOutcome(d)), d.ID}, func(v []*C.char) { C.ui_feedback_delivery(m.ui, v[0], v[1], v[2]) })
	}
	cstrings([]string{visible(f.headline())}, func(v []*C.char) { C.ui_feedback(m.ui, v[0], boolean(f.Visible), boolean(f.Expanded)) })
}
