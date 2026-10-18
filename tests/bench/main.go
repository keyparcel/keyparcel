//go:build e2e

// This fixture worker deliberately calls production Session operations unchanged.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path"
	"reflect"
	"sort"
	"time"

	"keyparcel/core"
	"keyparcel/tests/e2e/connection"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "benchmark failed:", err)
		os.Exit(1)
	}
}
func run() error {
	suite := flag.String("suite", "", "small or bulk")
	rtt := flag.Int("rtt", 0, "configured RTT ms")
	revision := flag.String("revision", "", "source commit")
	flag.Parse()
	if *suite != "small" && *suite != "bulk" {
		return errors.New("invalid benchmark suite")
	}
	payloadBytes := 512
	if *suite == "bulk" {
		switch *rtt {
		case 20:
			payloadBytes = 10 * 1024 * 1024
		case 100:
			payloadBytes = 2 * 1024 * 1024
		default:
			return errors.New("bulk requires 20 or 100 ms RTT")
		}
	}
	cfg, err := core.LoadConfig("config.json")
	if err != nil {
		return err
	}
	host := cfg.Hosts["parcel-test"]
	host.Root = path.Join(host.Root, "benchmark-"+*suite)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	dial := func() (*core.Session, error) { return connection.Connect(ctx, "parcel-test", host, "ssh_config", true) }
	backend := os.Getenv("KEYPARCEL_TEST_TRANSPORT")
	count := 5
	if *suite == "bulk" {
		count = 3
	}
	measure := func(name string, n int, prepare func() error, op func() error, check func() error) error {
		values := make([]time.Duration, 0, n)
		failures := 0
		for i := 0; i < n; i++ {
			if prepare != nil {
				if err := prepare(); err != nil {
					return fmt.Errorf("%s preparation: %w", name, err)
				}
			}
			start := time.Now()
			err := op()
			elapsed := time.Since(start)
			if err == nil && check != nil {
				err = check()
			}
			if err != nil {
				failures++
				fmt.Printf("%s\t%d\t%s\t%d\t%d\tFAIL\tFAIL\t%d\t-\n", backend, *rtt, name, n, len(values), failures)
				return fmt.Errorf("%s: %w", name, err)
			}
			values = append(values, elapsed)
		}
		var total time.Duration
		for _, v := range values {
			total += v
		}
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
		goodput := "-"
		if *suite == "bulk" && name != "setup" {
			goodput = fmt.Sprintf("%.3f", float64(payloadBytes)*8/(total.Seconds()/float64(n))/1e6)
		}
		fmt.Printf("%s\t%d\t%s\t%d\t%d\t%.3f\t%.3f\t%d\t%s\n", backend, *rtt, name, n, len(values), total.Seconds()*1000/float64(n), values[n/2].Seconds()*1000, failures, goodput)
		return nil
	}
	fmt.Printf("commit=%s suite=%s bytes=%d backend=%s current production transport settings; durations=ms\n", *revision, *suite, payloadBytes, backend)
	fmt.Println("backend\tRTT_ms\toperation\tplanned\tcompleted\tmean_ms\tmedian_ms\tfailures\tgoodput_Mbit_s")
	var fresh *core.Session
	if err = measure("setup", count, nil, func() error { var e error; fresh, e = dial(); return e }, func() error { return fresh.Close() }); err != nil {
		return err
	}
	s, err := dial()
	if err != nil {
		return err
	}
	defer s.Close()
	defer s.Client.RemoveAll(s.Root)
	discovery, err := s.DiscoverSFTP(cfg)
	if err != nil {
		return err
	}
	payload := make([]byte, payloadBytes)
	_, _ = rand.New(rand.NewSource(1)).Read(payload)
	filename := "benchmark.bin"
	var id string
	send := func(cached bool) error {
		var devices []core.Device
		if cached {
			devices = discovery
		}
		deliveries, e := s.SendWithDiscovery(cfg, []string{"self"}, "file", &filename, bytes.NewReader(payload), devices)
		if e != nil {
			return e
		}
		if len(deliveries) != 1 || deliveries[0].Error != "" || deliveries[0].Unconfirmed {
			return fmt.Errorf("unexpected delivery outcome: %+v", deliveries)
		}
		id = deliveries[0].ID
		return nil
	}
	remove := func() error {
		n, rejected, e := s.Delete([]string{id}, false)
		if e == nil && (n != 1 || len(rejected) != 0) {
			e = errors.New("incorrect deletion outcome")
		}
		return e
	}
	output := "benchmark-output.bin"
	defer os.Remove(output)
	receiveMethod := s.ReceiveSFTP
	receive := func() error {
		_, e := receiveMethod(cfg, id, func(m core.Manifest, f *os.File) error {
			if m.ID != id || m.Size != int64(len(payload)) || m.Type != "file" || m.Filename == nil || *m.Filename != filename {
				return errors.New("verified metadata differs")
			}
			_, e := core.SaveContent(output, m, f, true)
			return e
		}, true)
		return e
	}
	verify := func() error {
		f, e := os.Open(output)
		if e != nil {
			return e
		}
		defer f.Close()
		data, e := io.ReadAll(f)
		if e == nil && !bytes.Equal(data, payload) {
			e = errors.New("verified saved bytes differ")
		}
		return e
	}
	if *suite == "bulk" {
		// Capability setup is outside persistent transfer timers. Strict calls
		// below must never label a fallback as command throughput.
		if _, err = s.DiscoverAccelerated(cfg); err != nil {
			return err
		}
		var ids []string
		if err = measure(fmt.Sprintf("upload-%dMiB", payloadBytes/(1024*1024)), count, nil, func() error { return send(false) }, func() error { ids = append(ids, id); return nil }); err != nil {
			return err
		}
		for _, method := range []string{"sftp", "ssh"} {
			next := 0
			receiveMethod = s.ReceiveSFTP
			if method == "ssh" {
				receiveMethod = s.ReceiveAccelerated
			}
			if err = measure(fmt.Sprintf("download-%s-%dMiB", method, payloadBytes/(1024*1024)), count, func() error { id = ids[next]; next++; return nil }, receive, func() error {
				if e := verify(); e != nil {
					return e
				}
				if method == "ssh" {
					return remove()
				}
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	}
	if err = measure("command-initial-discovery", 1, nil, func() error { _, e := s.DiscoverAccelerated(cfg); return e }, nil); err != nil {
		return err
	}
	for _, method := range []string{"sftp", "ssh"} {
		if err = measure("discovery-"+method, count, nil, func() error {
			discover := s.DiscoverSFTP
			if method == "ssh" {
				discover = s.DiscoverAccelerated
			}
			d, e := discover(cfg)
			if e == nil && !reflect.DeepEqual(d, discovery) {
				e = errors.New("discovery paths differ")
			}
			return e
		}, nil); err != nil {
			return err
		}
	}
	if err = s.Client.MkdirAll(s.InboxDir()); err != nil {
		return err
	}

	for _, entries := range []int{0, 1, 10} {
		if entries > 0 {
			for i := 0; i < entries; i++ {
				if err = send(true); err != nil {
					return err
				}
			}
		}
		var expected []core.Item
		if err = measure(fmt.Sprintf("inbox-sftp-%d", entries), count, nil, func() error {
			items, rejected, ids, e := s.ListSFTP(cfg)
			if e == nil && (len(items) != entries || len(rejected) != 0 || len(ids) != entries) {
				e = errors.New("incorrect verified SFTP inbox")
			}
			expected = items
			return e
		}, nil); err != nil {
			return err
		}
		if err = measure(fmt.Sprintf("inbox-accelerated-%d", entries), count, nil, func() error {
			items, rejected, ids, e := s.ListAccelerated(cfg)
			if e == nil && (!reflect.DeepEqual(items, expected) || len(rejected) != 0 || len(ids) != entries) {
				e = errors.New("accelerated inbox differs from verified SFTP inbox")
			}
			return e
		}, nil); err != nil {
			return err
		}

		if entries > 0 {
			if _, rejected, e := s.Delete(nil, true); e != nil || len(rejected) != 0 {
				return fmt.Errorf("inbox cleanup: %v %v", e, rejected)
			}
		}
	}
	if err = send(true); err != nil {
		return err
	}
	for _, method := range []string{"sftp", "ssh"} {
		receiveMethod = s.ReceiveSFTP
		if method == "ssh" {
			receiveMethod = s.ReceiveAccelerated
		}
		if err = measure("receive-full-ID-"+method, count, nil, receive, verify); err != nil {
			return err
		}
	}
	if err = remove(); err != nil {
		return err
	}
	if err = measure("delete-full-ID", count, func() error { return send(true) }, remove, func() error {
		_, e := s.Client.Lstat(path.Join(s.InboxDir(), id+".age"))
		if !errors.Is(e, os.ErrNotExist) {
			return fmt.Errorf("deleted parcel remains: %v", e)
		}
		return nil
	}); err != nil {
		return err
	}
	for _, cached := range []bool{false, true} {
		for _, missing := range []bool{false, true} {
			name := fmt.Sprintf("send-cached=%t-missing=%t", cached, missing)
			prepare := func() error {
				if missing {
					return s.Client.RemoveDirectory(s.InboxDir())
				}
				return s.Client.MkdirAll(s.InboxDir())
			}
			if err = measure(name, count, prepare, func() error { return send(cached) }, func() error {
				if e := receive(); e != nil {
					return e
				}
				if e := verify(); e != nil {
					return e
				}
				return remove()
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
