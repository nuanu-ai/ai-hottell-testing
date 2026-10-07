import type { components } from '../../shared/api';

type IssuedMcpKey = components['schemas']['IssuedMcpKey'];

// Both fragments are those of docs/specs/hottell-contract/mcp.md, «Имя сервера и конфиги
// агентов». The key is [A-Za-z0-9_-]+, so it goes into the shell quotes and the TOML string
// as it is.

/** The command that adds the server to Claude Code in the user scope. */
export function claudeCodeCommand({ key, mcpUrl, serverName }: IssuedMcpKey): string {
  return (
    `claude mcp add --scope user --transport http ${serverName} ${mcpUrl} \\\n` +
    `  --header "Authorization: Bearer ${key}"`
  );
}

/** The block for ~/.codex/config.toml: Codex keeps the key in http_headers. */
export function codexConfigBlock({ key, mcpUrl, serverName }: IssuedMcpKey): string {
  return (
    `[mcp_servers.${serverName}]\n` +
    `url = "${mcpUrl}"\n` +
    `http_headers = { Authorization = "Bearer ${key}" }`
  );
}
