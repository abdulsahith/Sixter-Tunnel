# Sixter Tunnel

**Secure connectivity. Application delivery. Infrastructure management.**

[Website](https://sixter.in) · [Issues](../../issues) · [Discussions](../../discussions)

Sixter Tunnel is a Go-based tunneling and infrastructure platform for making local or privately hosted applications accessible through public endpoints. It combines HTTP/WebSocket forwarding, subdomain routing, and Linux deployment workflows, with a broader vision of bringing application delivery and infrastructure operations into one platform.

> **Project status:** Active development. The tunneling and Sixter Repo workflows described below have been implemented in the project's proof of concept. The CI/CD automation, broader cloud integrations, and multi-node orchestration described in the roadmap are not presented as production-ready features.

## Why Sixter?

Developers often need several independent tools to expose local services, configure DNS and TLS, deploy applications, access servers, and monitor running workloads. Sixter explores a unified workflow that connects these tasks, starting with a tunneling client/server architecture and an application-management component called **Sixter Repo**.

## Implemented capabilities

| Component | Capabilities |
| --- | --- |
| **Sixter Tunnel** | Persistent client/server tunnel connections, HTTP and WebSocket forwarding, subdomain-based access to hosted applications |
| **Routing and HTTPS** | Nginx reverse proxy integration, DNS/subdomain mapping, and TLS configuration in deployed environments |
| **Sixter Repo** | Project upload and management, server-side application execution, tunnel start/stop operations |
| **Remote operations** | Browser-based terminal access over WebSockets and performance/status endpoints |
| **Access control** | Token-based authentication in management APIs |
| **AMM runtime** | Experimental workload execution with optional process/filesystem isolation and Linux CPU/memory controls; verify supported features against the current AMM implementation before deploying |

Actual features may depend on the version and deployment configuration. This README is an architectural overview, not a guarantee that every component is available in every release.

## Architecture

```text
                        Public users / clients
                                 |
                           HTTPS / DNS
                                 |
                         Nginx reverse proxy
                                 |
                         Sixter tunnel server
                                 |
                       Persistent WebSocket
                                 |
                          Sixter client/agent
                                 |
                       Local HTTP / WS service

   Developer ---> Sixter Repo ---> Application execution
                       |                   |
                  Project upload      AMM (experimental)
                       |
                 Monitoring / terminal
```

### Request flow

1. A developer runs an application on a local machine or Linux server.
2. The Sixter client establishes a connection to the remote tunnel server.
3. A configured hostname or subdomain identifies the target application.
4. The reverse proxy and tunnel server route incoming requests through the tunnel.
5. The client forwards traffic to the corresponding local application and returns the response.

This arrangement can provide public access without directly exposing the application's listening port to the internet. Access controls and security settings still matter: a tunnel does not automatically make the application safe.

## Core components

### 1. Sixter Tunnel

The Go-based tunnel component is intended for developers who need to share development servers, expose web applications, or route traffic to services running behind NAT or private networks.

- HTTP request/response forwarding
- WebSocket forwarding
- Hostname and subdomain mapping
- Client/server tunnel sessions
- Reverse proxy and HTTPS integration

### 2. Sixter Repo

Sixter Repo provides a deployment and management layer alongside tunneling.

- Upload and manage application projects
- Start and manage hosted services
- Start or stop associated tunnels
- Open remote terminal sessions over WebSockets
- Inspect application/server performance information

Some deployments expose management endpoints such as `/api/projects`, `/api/start-tunnel`, `/api/stop-tunnel`, `/api/performance`, and `/api/terminal/<project>`. Endpoint availability and authentication requirements depend on the deployed version.

### 3. DNS, reverse proxy, and TLS

Sixter integrates domain-based routing with reverse proxy configuration. In deployed environments, Nginx routes incoming requests to the appropriate tunnel or application endpoint, while DNS records point hostnames toward the public entry point. TLS certificates protect HTTPS traffic at the configured termination point.

**Security note:** Never commit DNS-provider API tokens, private keys, certificates, or production secrets to the repository.

### 4. AMM: experimental application/container runtime

AMM is an evolving runtime component for application execution and resource management within the Sixter ecosystem. Its design explores:

- Application process lifecycle management
- Optional process and filesystem isolation
- Linux kernel-enforced CPU and memory resource limits
- Workload monitoring and integration with Sixter's control interface

AMM should not be assumed to provide full Docker/OCI compatibility or hardened multi-tenant isolation. OCI-compatible execution and multi-node orchestration are development goals, not completed features.

## Technology stack

- **Language:** Go; supporting Python and web components where applicable
- **Networking:** HTTP, HTTPS, TCP/IP, WebSockets, DNS
- **Infrastructure:** Debian/Linux, Nginx, systemd
- **Data:** PostgreSQL where configured
- **Operations:** Reverse proxy configuration, deployment management, performance monitoring

## Getting started

The installation process depends on the release and operating system. Do not execute unverified scripts as root.

1. Review the repository contents and available release artifacts.
2. Obtain the appropriate Sixter client or server build from an official release or trusted build process.
3. Configure the tunnel server endpoint, authentication credentials, and desired hostname.
4. Start the local application and establish a tunnel connection.
5. Verify that the configured public hostname routes to the expected application.

**Note for contributors:** This repository intentionally ignores `go.mod` and `go.sum` at the owner's request. Those files are usually committed in Go projects for reproducible builds. Until the project includes a verified dependency/bootstrap guide, cloning the repository alone may not be sufficient to build it.

### Deployment prerequisites

- A supported Linux host for server-side components
- Appropriate DNS records for the public hostname
- Reverse proxy and TLS configuration where required
- Network access between the tunnel client and tunnel server
- Authentication credentials stored outside version control

**Configuration examples:** Add sanitized example configuration files (such as `.env.example`) once the supported environment variables and CLI flags have been verified against the current code. Avoid publishing invented or outdated commands.

## Development roadmap

These items describe planned work, not generally available functionality:

- [ ] Source-triggered CI/CD pipelines with build, test, and deployment stages
- [ ] Expanded DNS and certificate lifecycle automation
- [ ] AMM runtime improvements and OCI-compatible workload support
- [ ] Multi-node workload scheduling and orchestration
- [ ] Cloud infrastructure provider integrations
- [ ] Enhanced observability, health checks, and traffic management
- [ ] More complete developer documentation and reproducible installation guides

### Intended CI/CD workflow

```text
Source repository
       |
   Build & test         (planned automation)
       |
  Package workload      (planned)
       |
 Sixter Repo / AMM
       |
 Deploy application
       |
 Tunnel + DNS routing
       |
 Monitor and operate
```

The goal is to reduce friction between source changes, application execution, public connectivity, and ongoing operations.

## Security considerations

- Protect tunnel and management authentication tokens.
- Apply least-privilege permissions to server processes and API credentials.
- Restrict remote terminal access to authorized users.
- Terminate HTTPS with valid certificates and keep proxy configuration current.
- Do not expose development/debug services to the public without authentication.
- Treat experimental runtime isolation as unsuitable for untrusted multi-tenant workloads until independently assessed.
- Report security vulnerabilities privately through the project's designated contact channel rather than posting exploit details in public issues.

## Contributing and issues

Bug reports and feature suggestions are welcome through GitHub Issues. Helpful bug reports include:

1. Operating system and Sixter version/commit
2. Expected and actual behavior
3. Steps to reproduce
4. Relevant sanitized logs (never include secrets)

Before accepting external contributions, add a `CONTRIBUTING.md`, code-of-conduct guidance, and a clear license.

## Project links

- **Website:** https://sixter.in
- **Repository:** This GitHub repository

## License

**License not yet specified in this README.** Add a `LICENSE` file before inviting reuse or redistribution. Publishing source code on GitHub does not automatically grant an open-source license.

---

*Sixter Tunnel — infrastructure without boundaries.*
