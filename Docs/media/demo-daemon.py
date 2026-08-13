#!/usr/bin/env python3
"""A stand-in daemon, so the docs screenshots show a machine worth screenshotting.

    python3 Docs/media/demo-daemon.py &
    GOGUMA_STATE_DIR=/tmp/ggdemo \
        macos/build/goguma.app/Contents/MacOS/goguma --render jobs-selected out.png light

`--render` normally reads the live daemon, which is the right default: it keeps
the pictures honest about what the app does with real data. It has two problems
for the jobs window specifically.

The first is that the author's machine is in the picture. The captured run had
real project names and a real home directory path in it, published in a README.

The second is that the columns were empty and correctly so. "Per sleep" is
`fires_per_night * battery_per_run`, and a job scheduled for 09:00 and 21:00
does not fire while anyone is asleep, so its honest answer is a dash. A whole
column of dashes teaches a reader nothing about what the column is for. Rather
than write numbers the product's own arithmetic would not produce, the fixture
below is a machine whose work runs overnight, which is the case goguma exists
for and the only one where that column has anything to say. `weekly-report` is
left firing on a Monday morning so one cell stays honestly blank.

The shape is captured from the running daemon rather than written out here, so
this cannot drift from the protocol; only the values are replaced.

It answers reads and nothing else. Starting a real second daemon would not do:
`paths.HelperSocket` is a compile-time constant, so it would reach the same
privileged helper as the real installation and fight it over the wake schedule.
"""
import copy, json, os, socket, struct, sys, threading

REAL = os.path.expanduser("~/Library/Application Support/goguma/daemon.sock")
DEMO_DIR = "/tmp/ggdemo"          # short: AF_UNIX caps the path at 104 bytes
SOCK = os.path.join(DEMO_DIR, "daemon.sock")

# name, cron, schedule display, short, battery/run, fires per night,
# typical, p95, ceiling, runs
DEMO = [
    ("nightly-backup", "0 3 * * *", "daily at 03:00", "daily",
     3.0, 1, "4m 12s", "5m 30s", "6m 36s", 34),
    ("db-dump", "30 2 * * *", "daily at 02:30", "daily",
     1.2, 1, "1m 48s", "2m 20s", "2m 48s", 41),
    ("photo-sync", "0 */4 * * *", "every 4h", "every 4h",
     0.6, 2, "52.4s", "1m 6s", "1m 19s", 96),
    ("calendar-sync", "0 6,11,15,20 * * *",
     "daily at 06:00, 11:00, 15:00 and 20:00", "4x daily",
     0.5, 2, "31.4s", "57.2s", "1m 8s", 112),
    ("weekly-report", "0 9 * * 1", "Mondays at 09:00", "Mondays",
     0.8, 0, "2m 5s", "2m 40s", "3m 12s", 9),
]


def call(op):
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    s.connect(REAL)
    b = json.dumps({"protocol": 1, "op": op}).encode()
    s.sendall(struct.pack(">I", len(b)) + b)
    n = struct.unpack(">I", s.recv(4))[0]
    buf = b""
    while len(buf) < n:
        buf += s.recv(n - len(buf))
    s.close()
    return json.loads(buf)


def build():
    caps = {op: call(op) for op in ("ping", "status", "jobs.list", "config.get")}
    jl = caps["jobs.list"]["payload"]
    if isinstance(jl, str):
        jl = json.loads(jl)
    src = sorted(jl["jobs"], key=lambda j: j["job"]["name"])
    out = []
    for i, (name, cron, disp, short, per_run, fires, typ, p95, ceil, runs) in enumerate(DEMO):
        j = copy.deepcopy(src[i % len(src)])
        j["job"].update(name=name, id=name, schedule=cron,
                        command=f"/usr/local/bin/{name}", source="crontab")
        j["schedule_display"], j["schedule_short"] = disp, short
        j["fires_per_night"] = fires
        # The daemon's own formula, so the two columns agree with each other.
        j["nightly_battery_pct"] = round(fires * per_run, 2)
        j["stats"].update(job_id=name, battery_per_run=per_run, runs=runs,
                          typical=typ, p95=p95, ceiling=ceil,
                          failures=0, never_detected=0)
        j["ceiling_reason"] = f"nearly the slowest of {runs} runs, plus 20%"
        out.append(j)
    jl["jobs"] = out
    caps["jobs.list"]["payload"] = jl

    # The config goes back to the shipped defaults.
    #
    # Whatever this machine happens to be set to ends up in a screenshot beside
    # a paragraph quoting config.Default(), and the two disagreeing is exactly
    # the drift TestReadmeSafetyNumbersMatchDefaults exists to catch. The
    # battery cutout here was 20 against a documented 10.
    cfg = caps["config.get"]["payload"]
    if isinstance(cfg, str):
        cfg = json.loads(cfg)
    cfg["config"].update({
        "wake_buffer": "1m 30s",
        "default_ceiling": "5m",
        "wake_only_hold": "3m",
        "thermal_cutout_c": 80,
        "low_battery_cutout_pct": 10,
    })
    caps["config.get"]["payload"] = cfg
    return caps


def main():
    caps = build()
    os.makedirs(DEMO_DIR, exist_ok=True)
    if os.path.exists(SOCK):
        os.unlink(SOCK)
    srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    srv.bind(SOCK)
    srv.listen(16)
    print(f"serving demo data on {SOCK}", flush=True)

    def handle(conn):
        try:
            while True:
                hdr = conn.recv(4)
                if len(hdr) < 4:
                    return
                n = struct.unpack(">I", hdr)[0]
                buf = b""
                while len(buf) < n:
                    c = conn.recv(n - len(buf))
                    if not c:
                        return
                    buf += c
                op = json.loads(buf).get("op")
                conn.sendall(frame(caps.get(op) or {"protocol": 1, "ok": True}))
        except Exception:
            pass
        finally:
            conn.close()

    def frame(obj):
        b = json.dumps(obj).encode()
        return struct.pack(">I", len(b)) + b

    while True:
        conn, _ = srv.accept()
        threading.Thread(target=handle, args=(conn,), daemon=True).start()


if __name__ == "__main__":
    sys.exit(main())
