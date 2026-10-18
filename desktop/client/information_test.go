package main

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func checkGTKInformationPointerAndKeyboard(t *testing.T, m *model) {
	t.Helper()
	await := func(description string, condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(4 * time.Second)
		for !condition() && time.Now().Before(deadline) {
			iterate()
			time.Sleep(time.Millisecond)
		}
		if !condition() {
			t.Fatal(description)
		}
	}
	// Keep dispatching native events while the real input process runs.
	drive := func(args ...string) {
		t.Helper()
		cmd := exec.Command("xdotool", args...)
		if err := cmd.Start(); err != nil {
			t.Fatalf("native input %v: %v", args, err)
		}
		finished := make(chan error, 1)
		done := false
		go func() { finished <- cmd.Wait() }()
		await("native input did not finish", func() bool {
			if done {
				return true
			}
			select {
			case err := <-finished:
				done = true
				if err != nil {
					t.Fatalf("native input %v: %v", args, err)
				}
				return true
			default:
				return false
			}
		})
	}
	defer func() {
		if t.Failed() {
			output, err := exec.Command("python3", "-c", `import gi,os;gi.require_version("Gdk","3.0");from gi.repository import Gdk;Gdk.init([]);w=Gdk.get_default_root_window();Gdk.pixbuf_get_from_window(w,0,0,w.get_width(),w.get_height()).savev(os.environ["KEYPARCEL_DESKTOP_TEST_SESSION"]+"/information-failure.png","png",[],[])`).CombinedOutput()
			if err != nil {
				t.Logf("native failure screenshot: %v %s", err, output)
			}
		}
	}()
	m.present()
	await("information control did not map", func() bool { return m.informationState("SSH target information").mapped })
	var window string
	await("mapped native test window absent", func() bool {
		windows, err := exec.Command("xdotool", "search", "--onlyvisible", "--pid", strconv.Itoa(os.Getpid()), "--name", "^keyparcel$").Output()
		ids := strings.Fields(string(windows))
		if err != nil || len(ids) == 0 {
			return false
		}
		window = ids[len(ids)-1]
		return true
	})
	drive("windowactivate", "--sync", window)
	flushUI()
	for _, name := range []string{"SSH target information", "Identity file information"} {
		state := func() informationState { return m.informationState(name) }
		bounds := state()
		t.Logf("%s window=%s native bounds=%+v", name, window, bounds)
		drive("mousemove", "--window", window, strconv.Itoa(bounds.x+bounds.width/2), strconv.Itoa(bounds.y+bounds.height/2))
		await(name+": native pointer hover absent", func() bool { return state().hovered })
		if state().open {
			t.Fatal(name + ": hover unexpectedly opened the click disclosure")
		}
		drive("mousedown", "1")
		await(name+": native pointer press absent", func() bool { return state().pressed })
		flushUI() // Run pending focus handlers before the physical release.
		drive("mouseup", "1")
		flushUI()
		await(name+": hover then click closed the explanation", func() bool { return state().open && state().focused })
		drive("mousemove", "1100", "900")
		if !state().open {
			t.Fatal(name + ": pointer leave dismissed clicked help")
		}
		drive("mousemove", "--window", window, strconv.Itoa(bounds.x+bounds.width/2), strconv.Itoa(bounds.y+bounds.height/2))
		drive("click", "1")
		await(name+": second click did not close help", func() bool { return !state().open })
		drive("mousemove", "1100", "900")
		drive("key", "Tab")
		await(name+": Tab did not leave control", func() bool { return !state().focused })
		drive("key", "shift+Tab")
		await(name+": keyboard focus did not return", func() bool { return state().focused })
		await(name+": keyboard focus did not show help", func() bool { return state().open })
		drive("key", "Escape")
		await(name+": Escape did not dismiss help", func() bool { return !state().open })
		drive("key", "space")
		await(name+": keyboard activation did not show help", func() bool { return state().open })
		drive("key", "space")
		await(name+": keyboard toggle did not close help", func() bool { return !state().open })
		drive("key", "space")
		await(name+": keyboard reopening failed", func() bool { return state().open })
		drive("key", "Tab")
		await(name+": focus leave did not close help", func() bool { return !state().focused && !state().open })
		if bounds.width < 24 || bounds.height < 24 || bounds.width > 36 || bounds.height > 36 {
			t.Fatalf("%s: expected compact usable target, got %dx%d", name, bounds.width, bounds.height)
		}
		if m.navigation.current != "settings" {
			t.Fatal("help dismissal navigated away from Settings")
		}
	}
}
