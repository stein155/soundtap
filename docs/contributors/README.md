# For contributors

Start with [CONTRIBUTING.md](../../CONTRIBUTING.md): setup, pull requests and
[the contract](../../CONTRIBUTING.md#the-contract), the rules for touching a speaker.

- [Technical design](design.md): what runs where, and the links to everything else.
- [Decision records](../decisions/README.md): why it is built this way.
- [State on `/mnt/nv`](nv-state.md): every file the speaker keeps across updates.
- [Environment variables](environment.md): every variable soundtapd and the init script read.
- [Reverse-engineering evidence](evidence.md): how to record what was read or measured.
- [Test map](testing/README.md): which tests cover what, and how to run them.
- [Install gate](testing/install-gate.md): the three layers every install route passes.

Per-model hardware facts, the release hardware test and the recovery tools are under
[hardware](../hardware/README.md). Releasing is in [RELEASING.md](../../RELEASING.md).
