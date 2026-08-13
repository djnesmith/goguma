#!/usr/bin/env python3
"""Build a throwaway HOME for the import recording, so it shows nobody's machine.

    python3 Docs/media/demo-home.py       # writes /tmp/goguma-demo
    HOME=/tmp/goguma-demo goguma import

`goguma import` scans the machine it runs on, which is the point of the command
and the problem with filming it: earlier takes published a real username, a real
tool's path, six real job names, and enough of them to name two projects and a
payment stack. The scanner resolves its sources from HOME, so pointing HOME at a
sandbox gives a genuine recording of the genuine command against invented jobs.

Written fresh each run rather than committed as a fixture, because the report is
built from how late each job has been: a file with fixed timestamps stops making
sense a week after it is written, and the jobs drop out of the report entirely.

/tmp/goguma-demo, not a path under the repo, because the source line prints in
full and a repo path contains the author's home directory again.
"""
import datetime, json, os, shutil

HOME = "/tmp/goguma-demo"
now = datetime.datetime.now().astimezone()
tz = now.tzinfo


def at(days_ago, hour, minute):
    d = (now - datetime.timedelta(days=days_ago)).date()
    return datetime.datetime(d.year, d.month, d.day, hour, minute, tzinfo=tz).isoformat()


def job(i, name, expr, display, ran_days_ago, ran_at, next_in_days, due):
    return {
        "id": f"job-{i}", "name": name, "enabled": True, "state": "scheduled",
        "schedule": {"kind": "cron", "expr": expr, "display": display},
        "schedule_display": display,
        "last_run_at": at(ran_days_ago, *ran_at),
        "next_run_at": at(-next_in_days, *due),
        "last_status": "ok", "paused_at": None, "fire_claim": None,
    }


# Scheduled overnight and demonstrably running late. That lateness is the whole
# report: goguma ranks by what has genuinely been firing while asleep, so jobs
# that always run on time are filtered out and would leave the demo empty.
JOBS = [
    job(1, "nightly-backup", "0 3 * * *",   "daily at 03:00",   1, (9, 14),  1, (3, 0)),
    job(2, "db-dump",        "30 2 * * *",  "daily at 02:30",   1, (8, 41),  1, (2, 30)),
    job(3, "log-prune",      "0 4 * * *",   "daily at 04:00",   2, (10, 6),  1, (4, 0)),
    job(4, "photo-sync",     "0 */4 * * *", "every 4h",         0, (12, 2),  0, (20, 0)),
    job(5, "weekly-report",  "0 9 * * 1",   "Mondays at 09:00", 6, (9, 0),   1, (9, 0)),
]

shutil.rmtree(HOME, ignore_errors=True)
os.makedirs(f"{HOME}/.hermes/cron", exist_ok=True)
with open(f"{HOME}/.hermes/cron/jobs.json", "w") as f:
    json.dump({"jobs": JOBS, "updated_at": now.isoformat()}, f, indent=1)
print(f"demo home at {HOME}")
