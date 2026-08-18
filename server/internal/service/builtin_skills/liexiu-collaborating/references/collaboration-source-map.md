# Collaboration source map

- Tool wire contract and seven operations:
  `server/pkg/protocol/orchestration_collaboration.go`
- Task-token identity derivation and HTTP boundary:
  `server/internal/handler/orchestration_collaboration.go`
- Operation-to-mailbox mapping:
  `server/internal/service/orchestration/runtime_collaboration.go`
- Mailbox envelope, bounds, and message types:
  `server/internal/service/orchestration/mailbox.go`
- Permission, reference, dedupe, Activity, and transaction behavior:
  `server/internal/service/orchestration/repository_mailbox.go`
- Cross-Runtime CLI adapter:
  `server/cmd/liexiu/cmd_collaborate.go`
