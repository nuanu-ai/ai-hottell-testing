import { claudeCodeCommand, codexConfigBlock } from './snippets';

const issued = {
  key: 'ht_mcp_0123456789abcdef',
  mcpUrl: 'http://localhost:8080/mcp',
  serverName: 'hottell',
};

// The expected text is copied from docs/specs/hottell-contract/mcp.md.
it('gives the Claude Code command of mcp.md', () => {
  expect(claudeCodeCommand(issued)).toBe(
    'claude mcp add --scope user --transport http hottell http://localhost:8080/mcp \\\n' +
      '  --header "Authorization: Bearer ht_mcp_0123456789abcdef"',
  );
});

it('gives the Codex block of mcp.md', () => {
  expect(codexConfigBlock(issued)).toBe(
    '[mcp_servers.hottell]\n' +
      'url = "http://localhost:8080/mcp"\n' +
      'http_headers = { Authorization = "Bearer ht_mcp_0123456789abcdef" }',
  );
});

it('takes the address and the server name from the answer', () => {
  const other = { key: 'k_1', mcpUrl: 'https://ht.example.test/mcp', serverName: 'other' };
  expect(claudeCodeCommand(other)).toContain(' other https://ht.example.test/mcp ');
  expect(codexConfigBlock(other)).toMatch(
    /^\[mcp_servers\.other\]\nurl = "https:\/\/ht\.example\.test\/mcp"/,
  );
});
