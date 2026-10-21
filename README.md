# MiniDock

Run Docker servers (PostgreSQL, Redis, MySQL, MongoDB and more) from templates, in an interactive terminal UI.
Pick a template, answer a short form, and the server is running. Works on macOS, Linux and Windows, and over SSH.

## Run it

```sh
go run ./cmd/minidock        # or: make run
make build                   # binary in dist/minidock
make build-all               # macOS, Linux and Windows binaries in dist/
```

Install it so you can type `minidock` anywhere:

```sh
make install                 # copies the binary to ~/.local/bin
make uninstall               # removes it again
```

You need Docker running (Docker Desktop, Colima, or the Docker service).
To manage a remote machine, set `DOCKER_HOST` (for example `ssh://user@host`) or run MiniDock on that machine over SSH.

## Keys

| Where | Key | Does |
|---|---|---|
| Everywhere | `tab` / `shift+tab`, `←` `→` | switch tab |
| Everywhere | `r` | re-check Docker and reload |
| Everywhere | `?` | help |
| Everywhere | `q` | quit |
| Templates | `↑` `↓` | choose a template |
| Templates | `/` | search |
| Templates | `enter` | set up a server from the template |
| Templates | `u` / `n` | duplicate as a custom template / new template |
| Templates | `i` / `x` | import / export a template JSON file |
| My Templates | `e` / `d` | edit / delete |
| Servers | `enter` | details and live logs |
| Servers | `s` | start or stop |
| Servers | `R` | restart |
| Servers | `c` | copy connection string |
| Servers | `m` | copy the command that gives an AI tool read-only access (MySQL/MariaDB/PostgreSQL) |
| Servers | `d` | delete (asks whether to keep the data) |
| External | `n` / `e` | add / edit a connection to a database MiniDock did not create |
| External | `t` | test the connection now |
| External | `m` | copy the command that gives an AI tool read-only access |
| External | `d` | remove the connection (the container is left alone) |

## Try each part

1. **Docker status** – the bottom bar shows `● Docker connected`. Stop Docker, press `r`: it turns red with advice.
2. **Templates** – the first tab lists PostgreSQL, Redis, MySQL, MongoDB (plus MariaDB and RabbitMQ as extra examples). Press `/` and type `redis`.
3. **Create a server** – press `enter` on PostgreSQL, fill the form, review the `docker run` command, press `enter` to create. Check with `docker ps`.
4. **Manage** – on the Servers tab try `s`, `R`, `c`, `enter` (live logs) and `d`.
5. **Your own template** – on My Templates press `n`, or press `u` on any template to start from a copy.
6. **A database you already run** – on the External tab press `n` and pick a MySQL container from another project.

## Let an AI tool read a database (read-only MCP)

MiniDock can give an AI coding tool **read-only** access to a MySQL, MariaDB or PostgreSQL database.
It writes the command for **Claude Code**, **Codex** or **Gemini CLI** — press `m` and pick one.
It can list databases and tables, show table columns and run `SELECT` queries. It cannot change anything.

This works for two kinds of database:

* a server MiniDock runs itself (the Servers tab), and
* one it did not create — a `docker compose` database, a colleague's stack, anything already running on your
  Docker host (the **External** tab).

### A server MiniDock runs

1. Create a MySQL, MariaDB or PostgreSQL server from a template and make sure it is running.
2. On the Servers tab select it and press `m` (also works in the details screen). Pick your tool, and MiniDock
   copies a command like:
   `claude mcp add minidock-shop-db -- /path/to/minidock mcp --server shop-db`
   Codex and Gemini CLI get the same line with their own program name.
3. Paste it in a terminal. In Claude Code open `/mcp` and check it says connected, then ask "list the databases".

### A database you already have

1. Start the database as you normally do, for example `docker compose up -d`.
2. Open the **External** tab and press `n`. MiniDock lists the containers it did not create; pick the one you want.
   Images that look like a database come first, with their compose project next to the name.
3. Give the connection a name and the login to use inside it. A user that may only read is a good idea —
   MiniDock refuses writes, but a read-only user means the database refuses them too.
4. Choose the client that matches the image — `psql` for PostgreSQL, `mariadb` for MariaDB.
5. Press `t` to try it right away. MiniDock reports how many databases that login can see, or why it could not connect.
6. Press `m`, pick your AI tool, and paste the copied command in a terminal.

The container is looked up by name every time a query runs, so recreating it (`docker compose up` again) keeps working.
Connections are saved as JSON in your config folder, in files only you can read, because they hold the password.
Removing a connection removes only the saved login — the container and its data are untouched.

Any other MCP client can use the same program: command `minidock`, arguments `mcp --server <name>`, where `<name>` is a
MiniDock server or a saved connection. Leave out `--server` when there is only one to choose from.

Tools: `list_databases`, `list_tables`, `describe_table`, `query`.

How "read-only" is enforced, in two layers:

* **A strict check** before anything is sent: one statement only, and it must be a read for that database
  (`SELECT`, `SHOW`, `EXPLAIN` and the like; `WITH` and `TABLE` on PostgreSQL). Comments, backslashes and more than one
  statement are always refused, as are `INTO`, `LOAD_FILE`, `FOR UPDATE` and `EXPLAIN ANALYZE`. On PostgreSQL that also
  covers `COPY`, dollar quoting (`$$`), the file-reading functions and anything that moves a sequence.
* **A read-only session**: MySQL and MariaDB run after `SET SESSION TRANSACTION READ ONLY`, PostgreSQL inside
  `BEGIN TRANSACTION READ ONLY`. The database itself refuses writes, even if the check above were bypassed.

Limits: 200 rows per query by default (1000 at most), 10 seconds per query, about 4 MB of raw output.
Queries run with `docker exec` and the `mysql`, `mariadb` or `psql` program inside the container, as the user you gave it
(for MiniDock's own servers, the one in the connection string, usually `root`), so it also works with a remote
`DOCKER_HOST`. The password is passed through the environment, not on the command line.

Not working? A stopped server says so; start it with `s`. "no MiniDock server or saved connection named ..." means the
name is misspelled (the message lists the right ones). For an external connection, press `t` on the External tab: it
says whether the container is missing, stopped, or the login is wrong.
If you used `go run`, the copied command points to a temporary program; run `make install` and press `m` again.

## Where your data lives

* Server data is kept in a Docker volume called `minidock-<name>-data`, so it survives deleting the container.
  Choose "delete container AND data" only if you want it gone.
* Servers only listen on `127.0.0.1` (your computer), not on the network.
* MiniDock marks its containers with the label `minidock.managed=true` and only shows those.
* Connections to databases MiniDock did not create are JSON files in the `external` folder next to your templates,
  written readable by you only, because they hold a password. MiniDock never changes those containers.
* Custom templates are JSON files in your config folder (`~/Library/Application Support/minidock/templates` on macOS,
  `~/.config/minidock/templates` on Linux, `%AppData%\minidock\templates` on Windows). Set `MINIDOCK_HOME` to change the base folder.

## Template format

A template is one JSON file. You can write it by hand, or use the editor in the app.

```json
{
  "id": "postgres",
  "name": "PostgreSQL",
  "icon": "🐘",
  "description": "Relational database.",
  "image": "postgres",
  "versions": ["17", "16"],
  "containerPort": 5432,
  "defaultHostPort": 5432,
  "dataPath": "/var/lib/postgresql/data",
  "fields": [
    { "key": "user", "label": "Username", "type": "text", "default": "postgres", "required": true },
    { "key": "password", "label": "Password", "type": "password", "required": true, "minLength": 4 }
  ],
  "env": { "POSTGRES_USER": "{{user}}", "POSTGRES_PASSWORD": "{{password}}" },
  "command": [],
  "connectionString": "postgresql://{{user}}:{{password}}@{{host}}:{{port}}/postgres"
}
```

* `versions`: image tags; the first is the default. `fields`: extra questions (`text`, `number`, `password`, `select` with `options`).
* `{{key}}` inserts an answer into `env`, `command` and `connectionString`.
  Always available: `{{name}}`, `{{version}}`, `{{port}}` (port on your computer) and `{{host}}` (`localhost`).
* Passwords are hidden in the review screen, and URL-escaped inside the connection string.
* Built-in templates live in `internal/template/builtin/` and are compiled into the binary.

## Development

```sh
make test               # unit and screen tests, no Docker needed
make test-integration   # real Docker: a Redis container lifecycle, and the read-only MySQL MCP (pulls mysql:8.4)
make vet fmt
```

Layout: `cmd/minidock` (entry), `internal/template` (templates, validation, rendering), `internal/docker` (Docker SDK wrapper),
`internal/tui` (screens), `internal/config` (paths), `internal/mcpserver` (read-only MySQL MCP).
