# NoX Backend

The storage and integration authority behind your NoX applications.

NoX Backend gives applications a shared place to store files, organize them into vaults and synchronize changes. Its dashboard lets you manage your account, API keys, vaults and Suite service connections without handling the underlying database directly.

It also provides the central registry and authorization used by NoX Suite. Application data stays under its owner's account, while connected services receive the authority needed for their own responsibilities.

## What you can do

| Capability | What it provides |
| --- | --- |
| Organize stored data | Create vaults and inspect the files committed to each vault. |
| Synchronize applications | Let compatible clients exchange file changes, detect conflicts and commit updates. |
| Control access | Manage personal API keys and account roles from the native dashboard. |
| Recover deleted vaults | Restore soft-deleted vaults or explicitly remove their data permanently. |
| Connect Suite services | Connect Yard and Gateway, inspect integration state and resume interrupted setup. |
| Use application APIs | Access storage and synchronization through HTTP APIs and supported MCP tools. |

Vaults and files are scoped to their owning account. Administrative access does not make another account's vaults your own. File contents use content-addressed storage, while SQLite holds metadata and operation state.

## How it fits into NoX Suite

- **Auth** handles Google sign-in and central account admission.
- **Backend** owns account/data relationships, personal API keys, service trust and the product registry.
- **Yard** installs and manages participating deployments.
- **Gateway** routes authorized MCP requests to the products that execute them.

Backend's native APIs and dashboard do not require Gateway or Yard. Dashboard sign-in uses NoX Auth. The frontend and backend are separate containers in the same project, allowing compatible frontend updates without rebuilding the backend service.

## Documentation

- [Installation and account setup](docs/user-setup.md)
- [Configuration](docs/backend-configuration.md)
- [Client API](docs/client-api.md) and [file reads/downloads](docs/read-api.md)
- [MCP tools](docs/mcp.md)
- [NoX Login](docs/nox-login.md)
- [Same-host Suite setup](docs/suite-local.md)
- [Administration API](docs/administration-api.md)
- [Frontend deployment](docs/frontend-deployment.md)
- [Updates, backups and recovery](docs/suite-delivery.md)
- [Troubleshooting](docs/troubleshooting.md)
- [Security reporting](SECURITY.md)

## Usage

Run NoX Auth on the same Docker host first. Save [docker-compose.yml](docker-compose.yml) and [.env.example](.env.example) in your deployment directory, naming the environment file `.env`.

Set `NOX_BACKEND_PUBLIC_URL` to the address you will open in your browser and `NOX_AUTH_PUBLIC_URL` to your Auth origin. Use your own addresses and deployment settings; private HTTP access is supported for Backend.

Start the project:

```sh
docker compose pull
docker compose up -d
```

Open your configured Backend address followed by `/vault-dashboard` and choose **NoX Login**. Create or select a vault and generate the personal API key needed by your client applications.

To connect Suite services, use **Service connections** in the dashboard. Enable Backend's Suite MCP exposure through `NOX_BACKEND_SUITE_MCP_ENABLED` when you want its tools available through Gateway. Follow the [Suite setup guide](docs/suite-local.md) for the shared local channel.

Keep the existing data volume when updating. Back up the complete Backend data directory, including its databases, stored files and private Suite state. See [delivery and recovery](docs/suite-delivery.md) before changing an existing installation.

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
