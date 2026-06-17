# nosy demo — SR Linux spine-leaf fabric

A 2-spine / 2-leaf SR Linux fabric running iBGP (AS 65000) over loopbacks,
for showing `nosy` querying live device state without YANG knowledge.

```
        spine1 (10.0.0.1)        spine2 (10.0.0.2)
         /            \           /            \
        /              \         /              \
   leaf1 (10.0.0.3)        leaf2 (10.0.0.4)
```

Every node peers iBGP with the spines/leaves it is cabled to, sourced from its
`system0` loopback. Static `/32` routes over each `/31` link make the loopbacks
reachable so the sessions establish.

## Prerequisites

- [containerlab](https://containerlab.dev/install/) (`clab version`)
- Docker, with the SR Linux image pullable: `docker pull ghcr.io/nokia/srlinux:24.10.1`
- `nosy` built and on your `PATH` (`go build ./cmd/nosy` from the repo root)

> **Note on the image tag:** `clab-nosy-demo.yaml` pins `ghcr.io/nokia/srlinux:24.10.1`
> rather than `:latest`. `:latest` drifts to different patch versions over time,
> which would change the firmware version nosy detects — and therefore which
> schema pack it needs. Pinning keeps the pack version predictable for the demo.

## Fetch schema pack

Before querying, nosy needs the **schema pack** for the target's NOS and
firmware version (`srl/24.10.1` here) cached locally. nosy validates every gNMI
path against it before issuing a GET — the schema gate — so a query with no pack
hard-stops instead of touching the device.

You don't have to look the version up by hand: **run any nosy query — if the
schema pack is missing, nosy will tell you exactly which version to fetch** in
the error it prints. So the simplest flow is spin up first (next section), then
fetch the version the error names. You can also read it from `clab inspect`.

**Internet-connected (default):**

```bash
nosy schema fetch --nos srl --version 24.10.1
```

**Air-gapped:**

```bash
# Copy the .bin across (USB/SCP), then install it directly:
nosy schema install <path-to-srl-24.10.1.bin>

# Or point nosy at an internal mirror that serves the packs:
nosy config set schema.server https://<internal-mirror>
```

Check what is cached and which source would be used, without fetching:

```bash
nosy schema list
nosy schema resolve --nos srl --version 24.10.1
```

## Spin up

```bash
cd demo
sudo clab deploy -t clab-nosy-demo.yaml
```

Nodes come up as `clab-nosy-demo-spine1`, `-spine2`, `-leaf1`, `-leaf2` — those
are the names you pass to `nosy --target`.

## Verify

```bash
# Topology and management IPs
sudo clab inspect -t clab-nosy-demo.yaml

# Confirm BGP is up (give it ~30s after deploy)
docker exec -it clab-nosy-demo-spine1 sr_cli "show network-instance default protocols bgp neighbor"
```

## Target profiles (skip the repeated credentials)

Rather than passing `--target clab-nosy-demo-spine1 --password NokiaSrl1!` on
every command, copy the demo profiles into place:

```bash
cp demo/targets.yaml ~/.nosy/targets.yaml
```

Queries then reference a node by its short profile name:

```bash
nosy query --target spine1 --intent bgp.neighbors
nosy query --target leaf1 "show interface counters"
```

Manage profiles with the `target` subcommand:

```bash
nosy target list            # show configured profiles
nosy target add edge1       # interactive: prompts for address/user/pass/insecure
nosy target test spine1     # capability probe → prints detected nos/version
nosy target remove edge1
```

Flags still win when you need a one-off override: `--username`, `--password`,
and `--insecure` take precedence over the profile's stored values.

## Demo query sequence

With `demo/targets.yaml` copied into `~/.nosy/`, the profile name is enough:

```bash
# BGP neighbors on spine1 (intent name)
nosy query --target spine1 --intent bgp.neighbors

# Interface counters on leaf1 (natural language)
nosy query --target leaf1 "show interface counters"

# BGP sessions in JSON for scripting
nosy query --target spine1 --intent bgp.neighbors --output json

# LLDP neighbors on spine1
nosy query --target spine1 --intent system.lldp

# Interface status across leaf1
nosy query --target leaf1 --intent interface.status
```

Without the profiles copied into place, pass the full container name and the
containerlab password explicitly instead, e.g.
`--target clab-nosy-demo-spine1 --password NokiaSrl1!`.

Or run all five with narration: `./demo.sh`.

## Tear down

```bash
cd demo
sudo clab destroy -t clab-nosy-demo.yaml --cleanup
```
