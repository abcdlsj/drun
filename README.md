# drun

A Go CLI tool for quickly restarting Docker containers with the latest image version.

## Features

- 🔄 One-command container restart with latest image
- 🎨 Colorful terminal output for better readability
- 🔍 Smart configuration preservation (ports, volumes, env vars, etc.)
- 🛡️ Interactive confirmation before execution
- 🧹 Automatic cleanup of system-generated environment variables

## Installation

### Build from source

```bash
git clone https://github.com/abcdlsj/drun.git
cd drun
go build -o drun main.go
```

### Install binary

Move the built binary to your PATH:

```bash
sudo mv drun /usr/local/bin/
```

## Usage

```bash
drun <container_name>
```

### Example

```bash
# Restart a container named 'my-web-app'
drun my-web-app
```

## How it works

1. **Inspect** - Gets the current container configuration using `docker inspect`
2. **Stop & Remove** - Stops and removes the existing container
3. **Pull Latest** - Pulls the latest version of the container's image
4. **Generate Command** - Reconstructs the docker run command with preserved configuration
5. **Confirm** - Shows the generated command and asks for user confirmation
6. **Execute** - Runs the new container with the same configuration

## What gets preserved

- Container name
- Port bindings (`-p` flags)
- Volume mounts (`-v` flags)
- Environment variables (`-e` flags, excluding system-generated ones)
- Restart policy (`--restart` flag)
- Network configuration (`--network` flag)
- Privileged mode (`--privileged` flag)
- Published ports (`-P` flag)
- Command and arguments

## What gets filtered out

The tool automatically filters out system-generated environment variables that shouldn't be reused:
- `PATH=`
- `HOSTNAME=`
- `HOME=`
- `TERM=`

## Output Colors

- 🔵 **Blue [INFO]** - Information messages
- 🟢 **Green [SUCCESS]** - Success messages  
- 🟡 **Yellow [WARNING]** - Warning messages
- 🔴 **Red [ERROR]** - Error messages
- 🔷 **Cyan** - Generated command display
- 🟨 **Yellow** - Interactive prompts

## Requirements

- Go 1.23.4 or later
- Docker installed and accessible
- Container must exist before running drun

## Error Handling

The tool provides clear error messages for common issues:
- Container not found
- Docker daemon not running
- Permission issues
- Image pull failures

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Test thoroughly
5. Submit a pull request

## License

MIT License - see LICENSE file for details