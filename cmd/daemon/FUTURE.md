# Future considerations

## Proxy hardening

1. [ ] Implement inbound authentication and transport security before supporting
  binding beyond loopback. The proxy currently does not authenticate callers;
  inbound and upstream provider credentials are already kept separate.
  llama-swap applies
  [authentication middleware to inference routes](https://github.com/mostlygeek/llama-swap/blob/8fa85899e424d47b81fa60aff08b238a793e2e2b/internal/server/server.go#L297-L325).
2. [ ] Supply configured upstream API keys through provider discovery rather
  than forwarding caller credentials. Adding, storing, and retrieving providers
  and their API keys are currently stubbed. llama-swap
  [injects independently configured peer credentials](https://github.com/mostlygeek/llama-swap/blob/8fa85899e424d47b81fa60aff08b238a793e2e2b/internal/router/peer.go#L293-L308).
