# HyperiOS

An intent-first Linux distribution. Talk to it. It figures out the rest.

HyperiOS is a custom Linux distribution (based on Ubuntu 24.04 LTS) where an AI agent is the primary interface. The OS exists to serve intent. Applications are infrastructure — installed, configured, surfaced, and hidden as needed to carry out commands.

## Status

Early development. See `docs/plan.md` for the full architecture and roadmap.

## Architecture

```
User (text / voice)
       |
[Hyperi Shell]  — persistent terminal interface
       |
[Intent -> Plan -> Risk Review -> Arbiter -> Execute]
       |
[Ubuntu 24.04 LTS + sway compositor]
```

The agent pipeline is safety-first: a deterministic, non-LLM Policy Arbiter has final say over every action. LLMs propose; the arbiter decides.

## Building

```bash
go build -o hyperi ./cmd/hyperi
go test ./...
```

**Linux only.** This is a distribution, not a cross-platform application.

## Running

```bash
# Dry-run (default): show what would happen
./hyperi session start "install vim and open a terminal"

# Execute the plan
./hyperi session start --execute "show git status"

# List sessions
./hyperi session list

# Resume a session
./hyperi session resume <session-id>

# Manage capabilities
./hyperi capability list
./hyperi capability grant execute:package apt:curl
```

## License

See LICENSE.
