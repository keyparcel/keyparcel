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
connector, with current production SFTP options: concurrent reads/writes and
up to 2 MiB transfer buffers. There are no benchmark-only transport tunings. Setup measures identity/config
loading, authentication and SFTP initialization; closing is outside that timer.
Remaining rows reuse one authenticated Session per backend/link cell. Shared
images build once, then embedded/OpenSSH cohorts run in parallel. Each owns a
separate SSH server, clients, private network, shaped endpoints and parcel root;
operations and measured samples remain sequential within that cohort. Image IDs
are immutable and no global fixture defaults change during fanout. Cohorts share
the runner's CPU/disk, so timings can include that contention; they do not share
a network cap or SSH connection.

Small uses five measured samples and valid signed/encrypted 512-byte file parcels:
strict SFTP/command discovery and full-ID receive comparisons, verified Inbox at
0/1/10 items through both SFTP and SSH, full-ID deletion, and all four
existing/missing-inbox × fresh/cached-discovery send cases. Preparation and cleanup
are outside operation timers. Receives include normal full verification and atomic
local SaveContent; exact saved bytes are checked. Sends include package creation
and atomic publication, then are received/checked/deleted outside the send timer.
Each small cell first records one `command-initial-discovery` sample (probe plus
first discovery). Command rows require the capability and never silently fall back;
verified inbox/discovery results must equal SFTP. There are 102 rows across both
transports/three RTTs: six initial samples and 96 rows of five samples.

Bulk uses three measured uploads and downloads per transport: 10 MiB at 20 ms
RTT and 2 MiB at 100 ms RTT, both at 100 Mbit/s. Uploads
use fresh command discovery and SFTP publication. Downloads compare strict SFTP
and SSH-command streams of the same uploaded parcels, fully verify and atomically
save them; deletion happens after both paths. Command capability preparation is
outside bulk timers. There are 16 bulk rows of three samples each. Payload bytes
are deterministic pseudorandom data, not zero fill.

All samples are retained, including the first; there are no discarded warmups.
Tables report commit, transport, requested RTT, planned/completed sample counts,
mean/median milliseconds and failures. Bulk goodput is plaintext Mbit divided by
mean operation seconds, including normal encryption/verification overhead. An error stops that cell immediately,
prints its incomplete count and fails the job. Table durations exclude provisioning,
calibration and cleanup; the **complete Actions job duration** is the CI runtime
budget measure. Shell, Go fixture and job timeouts bound failures. Summaries also
appear in Actions; normal stdout is sufficient without an artifact download.

The whole-job budget is five minutes **including** provisioning, calibration and
cleanup. This is a review/merge criterion, not an operation-performance assertion
or a required CI gate.
