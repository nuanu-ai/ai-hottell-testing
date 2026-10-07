# Public Hottell self-hosted distribution

This repository contains the server, frontend, migrations, macOS client and skills
from the upstream revision in SOURCE.json, with the listed public-distribution
adaptations. It must work without a private repository, our hosted service,
Gitea tokens, or knowledge from a previous agent session.

Use a feature branch and PR. Preserve unrelated changes. Keep README, Compose,
client version, download URLs and SOURCE.json consistent. Never publish local
configs, credentials, transcripts, fixtures or private deployment infrastructure.
Only runtime code is imported from upstream; new deployment examples must use
generated passwords and operator-supplied addresses.

Check the Docker build and Compose configuration. Smoke-test an isolated project:
migrations, first user, sign-in, invitation, MCP, authenticated OTLP ingestion,
analytics, bundled installer/downloads and persistence. Stop and remove only your
own test containers and volumes afterwards. Never run client install, uninstall
or restore against the host's working profile for distribution verification.
