# Production transport measurements

The regular PR/main workflow has two independent, informational jobs: `small`
and `bulk`. Neither is a dependency of a required aggregate; no repository
protections are changed. Setup/correctness errors fail the job visibly. There are
no performance pass/fail thresholds, required downloads or plots.

Run locally on an otherwise idle Linux Docker host:

```sh
bash tests/bench/run.sh small
bash tests/bench/run.sh bulk
```

The existing E2E SSH world supplies disposable identities, trust/configuration,
clients, server, private network and cleanup. Only its owned endpoint containers
receive NET_ADMIN, network tools and `tc` changes. Each endpoint's eth0 egress has
half the target RTT and a 100 Mbit/s cap, FIFO limit 10000, with TSO/GSO/GRO off.
No host/shared interfaces change. Idle ping checks RTT; four-flow iperf3 reports
actual receiver bandwidth in both directions. The qdisc readback is printed.
Calibration is not a guarantee of loaded RTT or single-flow throughput.

A native Go fixture worker runs as the existing ordinary client user. It calls
production `core.Session` methods through the existing embedded/OpenSSH test
connector, with current production SFTP options. Setup measures identity/config
loading, authentication and SFTP initialization; closing is outside that timer.
Remaining rows reuse one authenticated Session per backend/link cell. Backends
run sequentially on the same shaped fixture, never competing for its link.

Small uses five measured samples and valid signed/encrypted 512-byte file parcels:
discovery, verified Inbox at 0/1/10 items, full-ID receive/delete, and all four
existing/missing-inbox × fresh/cached-discovery send cases. Preparation and cleanup
are outside operation timers. Receives include normal full verification and atomic
local SaveContent; exact saved bytes are checked. Sends include package creation
and atomic publication, then are received/checked/deleted outside the send timer.
Bulk uses three measured 10 MiB uploads and downloads, at 20/100 ms RTT. Uploads
use fresh discovery. Downloads verify and atomically save the actual uploaded
parcels. Payload bytes are deterministic pseudorandom data, not zero fill.

All samples are retained, including the first; there are no discarded warmups.
Tables report commit, transport, requested RTT, planned/completed sample counts,
mean/median milliseconds and failures. Bulk goodput is plaintext Mbit divided by
mean operation seconds, including normal encryption/verification overhead. An error stops that cell immediately,
prints its incomplete count and fails the job. Table durations exclude provisioning,
calibration and cleanup; the **complete Actions job duration** is the CI runtime
budget measure. Shell, Go fixture and job timeouts bound failures. Summaries also
appear in Actions; normal stdout is sufficient without an artifact download.
