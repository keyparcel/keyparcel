package bridge

import (
	"context"
	"encoding/json"
	"keyparcel/core"
	"testing"
	"time"
)

func TestRequestCapturesApprovalsAndTimeout(t *testing.T) {
	owner := &Client{ctx: context.Background(), cancel: func() {}, config: &core.Config{Peers: map[string]string{}}, requestTimeout: time.Second}
	owner.connection = core.NewConnection(owner.ctx, func(context.Context) (*core.Session, error) {
		t.Fatal("request construction dialed SSH")
		return nil, nil
	})
	defer owner.Close()
	key, err := GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	public, err := PublicKey(key)
	if err != nil {
		t.Fatal(err)
	}
	peers, _ := json.Marshal(map[string]string{"approved": public})
	first, err := owner.NewRequest(string(peers), 5000, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := owner.NewRequest(`{}`, 9000, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.SetPeers(`{}`); err != nil {
		t.Fatal(err)
	}
	if first.connection != owner.connection || second.connection != owner.connection {
		t.Fatal("request created another transport owner")
	}
	if first.config.Peers["approved"] != public || len(second.config.Peers) != 0 || first.requestTimeout != 5*time.Second || second.requestTimeout != 9*time.Second || !first.background || second.background {
		t.Fatal("request snapshots leaked settings across operations")
	}
	if first.SetPeers(`{}`) == nil || first.SetRequestTimeout(1) == nil || first.Approve("other", public) == nil {
		t.Fatal("request settings were mutable")
	}

	first.CancelRequest()
	defer second.CancelRequest()
	if first.ctx.Err() != context.Canceled || second.ctx.Err() != nil || owner.ctx.Err() != nil {
		t.Fatal("request cancellation leaked into owner or another snapshot")
	}
	if err := first.CheckConnection(); err != context.Canceled {
		t.Fatal("canceled request reached admission", err)
	}

}
