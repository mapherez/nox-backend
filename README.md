# NoX Backend

A shared home for your NoX application data.

NoX Backend stores and synchronizes files for compatible applications. Organize data into vaults, manage your API key and connect your NoX Suite services from a browser dashboard.

- Create vaults and inspect their stored files.
- Synchronize changes between compatible applications, with conflict detection.
- Restore deleted vaults or permanently remove them.
- Manage accounts, API keys and Suite service connections.
- Use Backend operations from NoX CLI or an MCP-compatible AI client.

You keep the data on your own Docker host. Each account's vaults stay separate. Backend can serve compatible applications without Yard or Gateway; those services add Suite deployment and access to tools.

## What you need

- Docker with Docker Compose on the server where Backend will run.
- [NoX Auth](https://github.com/mapherez/nox-auth) running on that same server, with your Google account allowed to sign in.
- An address you can open in your browser. Private HTTP access is supported; Backend does not require you to create certificates for local use.

## Help

[Setup guide](docs/user-setup.md) · [Configuration](docs/backend-configuration.md) · [Backups and recovery](docs/suite-delivery.md) · [Troubleshooting](docs/troubleshooting.md)

For app developers: [client API](docs/client-api.md) · [MCP](docs/mcp.md) · [Suite integration](docs/suite-local.md)

## Usage

### Install

On your Docker server, download the deployment files into a new folder:

```sh
mkdir nox-backend
cd nox-backend
curl -fsSLo docker-compose.yml https://raw.githubusercontent.com/mapherez/nox-backend/master/docker-compose.yml
curl -fsSLo .env https://raw.githubusercontent.com/mapherez/nox-backend/master/.env.example
```

Open `.env` in a text editor and set:

| Setting | What to enter |
| --- | --- |
| `NOX_BACKEND_PUBLIC_URL` | The exact Backend address you will open in your browser, including its port when needed. |
| `NOX_AUTH_PUBLIC_URL` | The HTTPS address of your running NoX Auth service. |

The supplied configuration uses host port **5710**. To change it, set `NOX_FRONTEND_PORT` and include that port in your Backend address. The other settings can keep their defaults for a new installation using Auth's default volume names. Google credentials belong in Auth, not Backend.

Start Backend:

```sh
docker compose pull
docker compose up -d
```

### Sign in and connect an app

Open your configured Backend address followed by `/vault-dashboard`. Choose **NoX Login** and sign in with your allowed Google account.

Create a vault and generate a personal API key from the dashboard. In a compatible app, enter your Backend base address and that key. Keep the key private; replacing it invalidates the previous key for clients using it.

### Connect your Suite

In **Service connections**, enter Yard's or Gateway's address, choose **Verify**, then **Connect** and confirm the verified identity. All Suite services must run on the same Docker host. The supplied Compose files share the local communication channel automatically; no certificate copying is needed.

To make Backend's own tools available through Gateway and CLI, set `NOX_BACKEND_SUITE_MCP_ENABLED=true` in `.env`, run `docker compose up -d`, then open the Backend dashboard. Refresh the catalog in your client afterward.

### Update and preserve data

From your installation folder:

```sh
docker compose pull
docker compose up -d
```

Keep the data volume and back it up before updating. The project runs a backend and a separate frontend together; an `auth-setup` container exiting successfully is normal. See [backups and recovery](docs/suite-delivery.md) for preserving the complete installation.

## License & Attribution

This project is licensed under the **Creative Commons Attribution-NonCommercial-ShareAlike 4.0 International (CC BY-NC-SA 4.0)** license.

### How to Credit This Project

If you use, fork, or modify this code, you **must** provide attribution by linking back to my GitHub profile. Please include the following credit notice prominently in your repository or project documentation:

> **Based on [nox-backend](https://github.com/mapherez/nox-backend) by [mapherez](https://github.com/mapherez)**
> This project includes code from [nox-backend](https://github.com/mapherez/nox-backend), used under the CC BY-NC-SA 4.0 license.

---

*For more details on what you can and cannot do, please refer to the [LICENSE](LICENSE) file in this repository.*

## Disclaimer of Warranty

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
