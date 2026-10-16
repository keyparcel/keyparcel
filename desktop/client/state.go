package main

import "keyparcel/core"

// A peer selection belongs to both the connection and the approved key.
func retainedRecipient(name, oldTarget string, oldHost core.Host, oldPeers map[string]string, p profile) string {
	if oldTarget != p.Target || oldHost != p.Host {
		return ""
	}
	if name == "self" {
		return name
	}
	if key, ok := oldPeers[name]; ok && key == p.Config.Peers[name] {
		return name
	}
	return ""
}

type navigation struct {
	current, settingsParent string
}

func (n *navigation) open(page string) {
	if page == "settings" && n.current != "settings" {
		n.settingsParent = n.current
		if n.settingsParent == "" || n.settingsParent == "text" || n.settingsParent == "saved" {
			n.settingsParent = "inbox"
		}
	}
	n.current = page
}

func (n navigation) parent() string {
	switch n.current {
	case "settings":
		return n.settingsParent
	case "text", "saved":
		return "inbox"
	}
	return ""
}

// Carry the refreshed profile back to the UI without mutating it on a worker.
type recipientChanged struct{ profile profile }

func (*recipientChanged) Error() string {
	return "Host or recipient approval changed; choose a recipient again"
}
