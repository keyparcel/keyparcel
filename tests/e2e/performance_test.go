//go:build e2e && performance

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTransportPerformance(t *testing.T) {
	suite := os.Getenv("KEYPARCEL_BENCH_SUITE")
	if suite != "small" && suite != "bulk" {
		t.Fatal("select small or bulk benchmark suite")
	}
	build, stop := context.WithTimeout(context.Background(), 2*time.Minute)
	defer stop()
	for _, role := range []string{"server", "client"} {
		out, diag, err := container(build, nil, "build", "-t", "keyparcel-bench-"+role+":local", "-f", filepath.Join(repo, "tests/bench/Dockerfile."+role), filepath.Join(repo, "tests/bench"))
		if err != nil {
			t.Fatalf("benchmark image: %v %s%s", err, out, diag)
		}
	}
	originalClient := clientImage
	clientImage = "keyparcel-bench-client:local"
	defer func() { clientImage = originalClient }()
	w := makeWorldWithOptions(t, false, "ed25519", "openssh", worldOptions{timeout: 9 * time.Minute, netAdmin: true, serverImage: "keyparcel-bench-server:local"})
	worker, err := os.ReadFile(os.Getenv("KEYPARCEL_BENCH_BINARY"))
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(w.alice.dir, "benchmark")
	if err = os.WriteFile(executable, worker, 0755); err != nil {
		t.Fatal(err)
	}
	root := func(id string, args ...string) []byte {
		t.Helper()
		out, diag, e := container(w.ctx, nil, append([]string{"exec", "--user", "0", id}, args...)...)
		if e != nil {
			t.Fatalf("owned network setup %v: %v %s%s", args, e, out, diag)
		}
		return out
	}
	root(w.id, "iperf3", "-s", "-D")
	rtts := []int{0, 20, 100}
	if suite == "bulk" {
		rtts = []int{20, 100}
	}
	fmt.Printf("suite=%s commit=%s link=100Mbit/s per-direction; isolated endpoint eth0 egress only; no random loss\n", suite, os.Getenv("KEYPARCEL_BENCH_REVISION"))
	for _, rtt := range rtts {
		for _, id := range []string{w.id, w.alice.id} {
			root(id, "ethtool", "-K", "eth0", "tso", "off", "gso", "off", "gro", "off")
			root(id, "tc", "qdisc", "replace", "dev", "eth0", "root", "netem", "delay", fmt.Sprintf("%.1fms", float64(rtt)/2), "rate", "100mbit", "limit", "10000")
			actual := root(id, "tc", "qdisc", "show", "dev", "eth0")
			if !strings.Contains(string(actual), "netem") || !strings.Contains(string(actual), "rate 100Mbit") {
				t.Fatalf("unexpected link settings: %s", actual)
			}
			fmt.Printf("RTT=%d endpoint=%s actual_qdisc=%s", rtt, map[bool]string{true: "server", false: "client"}[id == w.id], actual)
		}
		ping := root(w.alice.id, "ping", "-c", "5", "-i", "0.05", "-w", "5", "inbox")
		match := regexp.MustCompile(`= [0-9.]+/([0-9.]+)/`).FindSubmatch(ping)
		if len(match) != 2 {
			t.Fatalf("missing ping RTT: %s", ping)
		}
		actualRTT, e := strconv.ParseFloat(string(match[1]), 64)
		if e != nil || math.Abs(actualRTT-float64(rtt)) > math.Max(5, float64(rtt)*0.25) {
			t.Fatalf("RTT verification target=%d actual=%f: %s", rtt, actualRTT, ping)
		}
		for _, reverse := range []bool{false, true} {
			args := []string{"iperf3", "-c", "inbox", "-t", "3", "-O", "1", "-P", "4", "-J"}
			if reverse {
				args = append(args, "-R")
			}
			var result struct {
				Error string `json:"error"`
				End   struct {
					Received struct {
						Rate float64 `json:"bits_per_second"`
					} `json:"sum_received"`
				} `json:"end"`
			}
			data := root(w.alice.id, args...)
			if e = json.Unmarshal(data, &result); e != nil || result.Error != "" {
				t.Fatalf("bandwidth verification: %v %s", e, data)
			}
			rate := result.End.Received.Rate / 1e6
			// Calibration checks the cap is active, not a performance regression threshold.
			if rate <= 0 || rate > 130 {
				t.Fatalf("100Mbit link cap not observed: %f", rate)
			}
			fmt.Printf("link target_RTT_ms=%d ping_mean_ms=%.3f direction=%s iperf_receiver_Mbit_s=%.3f flows=4 omitted_s=1 measured_s=3\n", rtt, actualRTT, map[bool]string{true: "download", false: "upload"}[reverse], rate)
		}
		for _, backend := range []string{"embedded", "openssh"} {
			out, diag, e := w.exec(w.alice, nil, "env", "KEYPARCEL_TEST_TRANSPORT="+backend, executable, "-suite", suite, "-rtt", strconv.Itoa(rtt), "-revision", os.Getenv("KEYPARCEL_BENCH_REVISION"))
			fmt.Print(string(out))
			fmt.Print(string(diag))
			if e != nil {
				t.Fatalf("production benchmark %s RTT%d: %v", backend, rtt, e)
			}
		}
	}
}
