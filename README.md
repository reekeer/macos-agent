# reekeer macOS agent

Runs ephemeral macOS GitHub Actions runners on a Mac with [Tart](https://github.com/openai/tart), managed from the reekeer panel.

Every job gets a fresh VM cloned from one image. When the job ends the VM is deleted.

## Requirements

- Apple Silicon Mac
- Tart and Softnet (`softnet` must be owned by root with the setuid bit)
- A logged-in user session (Tart runs VMs in the user session)
- Power adapter connected, sleep disabled: `sudo pmset -a sleep 0 disablesleep 1`

## Install

Add the Mac in the panel (Runners → Hosts → Mac) and run the command it shows:

```sh
curl -fsSL https://raw.githubusercontent.com/reekeer/macos-agent/main/install.sh | sh -s -- --panel https://panel.example --token rka_…
```

The agent installs to `~/Library/Application Support/reekeer-agent` and runs as the `io.reekeer.agent` LaunchAgent.

## How it works

- The agent reports to the panel every 5 seconds and gets back the pools assigned to this Mac.
- For each pool it clones the image, starts the VM with isolated networking (`--net-softnet`), asks the panel for a just-in-time runner config and starts the runner.
- After the job the VM is stopped and deleted.
- Each pool has its own cache folder mounted into the VM (Homebrew, npm, pip, yarn, pnpm, Gradle, CocoaPods), trimmed to the size set in the panel.
- Leftover VMs, old images and caches of removed pools are cleaned up automatically.
- New releases of the agent are installed automatically when no job is running.

## Uninstall

```sh
~/Library/Application\ Support/reekeer-agent/reekeer-agent uninstall
```

## Build

```sh
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o reekeer-agent .
```

Releases are built by GitHub Actions on `v*` tags.
