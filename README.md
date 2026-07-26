# drun

Safely refresh a Docker container with the latest image while preserving its runtime configuration.

<p align="center">
  <img src="assets/demo.gif" width="900" alt="drun terminal demo showing the latest image pull and safe container replacement">
</p>

## Why drun

Updating a one-off Docker container usually means digging through `docker inspect`, rebuilding a long `docker run` command, and hoping nothing was missed. `drun` turns that into one command while keeping the old container available as a rollback point until the replacement is running.

- Pulls the latest version of the configured image
- Shows the exact replacement command before changing the container
- Preserves ports, volumes, environment variables, labels, restart policy, capabilities, networks, entrypoint, user, working directory, and common security options
- Restores the original container automatically when replacement fails
- Uses a Bubble Tea interface with animated status, focused choices, and rich terminal styling
- Supports non-interactive automation and plain-text output

## Install

Go 1.24 or later and a working Docker CLI are required.

```bash
go install github.com/abcdlsj/drun@latest
```

Or build the repository locally:

```bash
git clone https://github.com/abcdlsj/drun.git
cd drun
go build -o drun .
```

## Usage

```console
$ drun [options] <container_name>
```

Common workflows:

```bash
# Preview the reconstructed command without pulling or changing anything
drun --dry-run my-web-app

# Refresh interactively
drun my-web-app

# Skip confirmation in automation
drun --yes my-web-app

# Disable colors and animated rendering
drun --no-color my-web-app
```

| Option | Description |
| --- | --- |
| `--dry-run` | Inspect and print the replacement command without making changes |
| `-y`, `--yes` | Skip the confirmation prompt |
| `--no-color` | Disable color and interactive rendering |
| `--version` | Print version information |

Set the standard `NO_COLOR` environment variable to disable color as well.

## Safe replacement

`drun` deliberately delays destructive work until after the image pull and confirmation:

1. Inspect the current container and reconstruct its configuration.
2. Print the exact `docker run` command.
3. Pull the latest image.
4. Ask for confirmation unless `--yes` is set.
5. Stop the current container and rename it to a temporary rollback name.
6. Start the replacement and reconnect additional networks.
7. Remove the rollback container only after the replacement succeeds.

If startup or network attachment fails, `drun` removes the partial replacement, restores the original name, and restarts the original container when it was previously running.

## Configuration coverage

The reconstructed container includes:

- Container name, image, command, and entrypoint
- Environment variables and labels
- Bind mounts, named volumes, and anonymous volumes
- Published ports, including host IP bindings
- Restart policy, including `on-failure` retry count
- Primary and additional networks with user-defined aliases
- User, working directory, capabilities, security options, DNS, extra hosts, and supplementary groups
- Privileged, read-only root filesystem, init, shared memory, tmpfs, and publish-all settings

Containers created with `--rm` are rejected because Docker deletes them when stopped, which makes a reliable rollback impossible.

## Development

```bash
go test -race ./...
go vet ./...
go build ./...
```

Regenerate the README animation with ImageMagick:

```bash
./scripts/generate-demo.sh
```
