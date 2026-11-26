# pgbench

The `pgbench` workload adapter runs PostgreSQL's
[pgbench](https://www.postgresql.org/docs/current/pgbench.html) against the
target. A step sets `type: pgbench` and one of two commands:

| `command` | Runs | Reports |
|---|---|---|
| `init` | `pgbench --initialize`: creates and loads the standard pgbench tables | nothing |
| `run` | a timed (`time`) or counted (`transactions`) benchmark | the summary pgbench prints at the end, and its per-second progress |

## Arguments

Every key in `args` becomes a pgbench long option, sorted by name: an empty
value is a bare flag (`no-vacuum: ""` → `--no-vacuum`), anything else
`--key=value`. So any pgbench option works without changes to benchctl:
`scale`, `client`, `jobs`, `time`, `transactions`, `protocol`, `builtin`,
`rate`, `max-tries`, `init-steps`, `partitions`, and so on.

Two exceptions:

- `script` is the text of a custom script. The adapter writes it to a scratch
  file and passes that with `--file`, so the scenario needs no file of its own
  on the driver instance. It applies to `run` only.
- `host`, `port`, `username`, `dbname` and `initialize` are rejected. The
  connection comes from the target's outputs and reaches pgbench through the
  `PG*` environment variables, which also keeps the password off the command
  line.

`run` passes `--progress=1` unless the step sets `progress` itself.

```yaml
steps:
  - name: init
    type: pgbench
    command: init
    args:
      scale: "{{ inputs.scale }}"
  - name: my-script
    type: pgbench
    command: run
    args:
      script: |
        \set aid random(1, 100000 * :scale)
        SELECT abalance FROM pgbench_accounts WHERE aid = :aid;
      scale: "{{ inputs.scale }}"   # sets :scale for the script
      client: "{{ inputs.clients }}"
      jobs: "{{ inputs.clients }}"
      time: "60"
      protocol: prepared
      no-vacuum: ""
```

A workload that needs its own schema or functions, rather than the standard
tables, creates them with `type: sql` steps before the `run` steps; see
[concepts.md](concepts.md#scenario-yaml-reference).

`scenarios/postgres-pgbench-local.yaml` is a complete local example. It
names no particular workload: the `builtin` and `script` inputs pick what
its two run steps execute, e.g.
`--set builtin=tpcb-like --set script="$(cat my-script.sql)"`.

## Metrics

A `run` step emits:

| Family | Labels | Value |
|---|---|---|
| `pgbench_tps` | | transactions per second, without initial connection time |
| `pgbench_transactions` | | transactions processed |
| `pgbench_failed_transactions` | | failed transactions (pgbench 15+) |
| `pgbench_latency_ms` | `stat`: `avg`, `stddev` | latency; `stddev` only with `--progress`, `--rate` or `--latency-limit` |
| `pgbench_connection_time_ms` | | initial connection time (pgbench 14+) |
| `pgbench_duration_seconds` | | `--time`, for timed runs |

Figures pgbench does not print are left out rather than reported as zero.
As with every workload, points also carry the `step` label and any
`fixture_*` labels.

The per-second progress reports go to `raw_samples_*.csv` (stdout collector)
with the columns `elapsed_s,tps,latency_avg_ms,latency_stddev_ms,failed`.
`benchctl results` shows one row per step and fixture combination.

The pgbench version is recorded as the `pgbench_version` run label.

## Installing pgbench

pgbench ships with the PostgreSQL server packages, not the client ones. The
adapter uses the `pgbench` on `PATH`, or else the newest one under
`/usr/lib/postgresql/*/bin` (Debian, Ubuntu) or Homebrew's `postgresql@*`
kegs.

- Locally: `mise run install-pgbench`.
- On a remote load driver: set `pgbench_enabled: true` in the driver's
  `vars` (`deployments/ec2/loaddriver_gotpc`, `deployments/gce/loaddriver_gotpc`).
  The driver then installs the distribution's `postgresql` package during
  bootstrap, without creating a cluster, and fails the bootstrap if that
  fails.

```yaml
driver:
  provider: ec2
  config:
    module: ./deployments/ec2/loaddriver_gotpc
    vars:
      pgbench_enabled: true
```

## Running pgbench on the target

The `ec2-target` driver provider runs the workload on the EC2 target
instance itself instead of on a load driver of its own, so pgbench reaches
the database without a network hop, as in most published pgbench results.
It provisions nothing: it hands the run to the target the way `ec2` hands it
to its driver instance, and leaves the instance to the target's teardown.

```yaml
target:
  provider: opentofu
  config:
    module: ./deployments/ec2/postgres
    vars:
      pgbench_enabled: true   # install pgbench on the target
      huge_pages_gb: 90       # optional, see below
driver:
  provider: ec2-target
  config:
    host: "{{ target.outputs.public_ip }}"
    ssh_private_key_path: "{{ target.outputs.ssh_private_key_path }}"
    instance_type: "{{ inputs.target_instance_type }}"   # picks the binary's architecture
```

- pgbench shares the CPUs with the database, so leave it headroom (fewer
  `jobs` than cores) or count it in when comparing.
- `between-benchmarks` cannot `reboot` the target: the run is driven from
  it. `benchctl validate` rejects the combination.
- `huge_pages_gb` reserves that much memory in huge pages at boot (and fails
  the bootstrap if the kernel cannot); a service uses them with
  `-c huge_pages=on`. Size it to the largest shared memory segment of any
  service, and remember the memory is unavailable for anything else.
