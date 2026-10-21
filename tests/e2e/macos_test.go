//go:build e2e && darwin

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCopyUsesNativeMacClipboard(t *testing.T) {
	// A hosted runner owns this pasteboard; never read a developer's clipboard.
	if !localFixtures() || os.Getenv("GITHUB_ACTIONS") != "true" {
		t.Skip("requires owned native GitHub runner")
	}
	w := newWorld(t, "openssh")
	snapshot, diag, err := w.exec(w.bob, nil, "/usr/bin/osascript", "-l", "JavaScript", "-e", `
ObjC.import('AppKit');
var result = [], items = $.NSPasteboard.generalPasteboard.pasteboardItems;
for (var i=0; i<items.count; i++) {
 var item=items.objectAtIndex(i), values={};
 for (var j=0; j<item.types.count; j++) {
  var type=item.types.objectAtIndex(j), data=item.dataForType(type);
  if (data) values[ObjC.unwrap(type)]=ObjC.unwrap(data.base64EncodedStringWithOptions(0));
 }
 result.push(values);
}
JSON.stringify(result);`)
	if err != nil {
		t.Fatalf("save owned pasteboard: %v %s", err, diag)
	}
	path := filepath.Join(w.bob.dir, "pasteboard.json")
	if err := os.WriteFile(path, snapshot, 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, diag, err := command(ctx, nil, "/usr/bin/osascript", "-l", "JavaScript", "-e", `
ObjC.import('AppKit');
function run(args) {
 var saved=JSON.parse(ObjC.unwrap($.NSString.stringWithContentsOfFileEncodingError(args[0], $.NSUTF8StringEncoding, null))), items=[];
 saved.forEach(function(values) {
  var item=$.NSPasteboardItem.alloc.init;
  Object.keys(values).forEach(function(type) {
   var data=$.NSData.alloc.initWithBase64EncodedStringOptions(values[type],0);
   if (!item.setDataForType(data,type)) throw Error('restore item type');
  });
  items.push(item);
 });
 var pb=$.NSPasteboard.generalPasteboard; pb.clearContents;
 if (items.length && !pb.writeObjects(items)) throw Error('restore pasteboard');
}`, path)
		if err != nil {
			t.Errorf("restore owned pasteboard: %v %s", err, diag)
		}
	})
	w.approve()
	checkClipboardTransfers(t, w, func() []byte { return w.clientOK(w.bob, nil, "/usr/bin/pbpaste", "-Prefer", "txt") })
}

func TestLocalWatchCancellation(t *testing.T) {
	if !localFixtures() {
		t.Skip("native fixture lifecycle coverage")
	}
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		op := startReusedWatch(t, w)
		w.interrupt(op)
		if status := w.wait(op); status != 0 {
			t.Fatalf("native watch cancellation: status %d", status)
		}
	})
}

func TestLocalSelectedIdentity(t *testing.T) {
	if !localFixtures() {
		t.Skip("native fixture identity coverage")
	}
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		denied := "Permission denied"
		if mode == "embedded" {
			denied = "unable to authenticate"
		}
		checkSelectedIdentity(t, w, denied)
	})
}
