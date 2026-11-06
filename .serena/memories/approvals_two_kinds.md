### Approvals: two kinds, one place (ui)

"Approval" means two unrelated things in Hermod:

- **Release** (internal/environment, `/api/environments/promotions` received, `/sent` with
  `Link.decide`): flow versions sent from another environment; an **Administrator** approves and
  they go live. A vhost a connection asks to add (`/vhost-requests`) is counted with them.
- **Held message** (internal/approval, `/api/approvals`, EditorOnly approve): one message an
  Approval step (or an AI agent write tool) paused; an **Editor** lets it continue.

Both live on `/approvals` (`pages/approvals/ApprovalsHubPage.tsx`), tabs `releases` / `held`
(`approvalsTabs` in nav.ts). Approvals is the sixth primary nav place, after Home, with a red badge.
`lib/approvals.ts` `waitingApprovals` is the single count (sidebar, bottom tabs, tab badges,
Home "Needs you" rows): only what this role may decide; a release you sent under
`admin_except_sender` is left out (case-insensitive, like the server); `deploying` is not counted.
`useWaitingApprovals` shares query keys with the Releases page (`environmentKeys.*`,
`['approvals','pending-count']`) so deciding anywhere updates the badge; polls every 60s; it is
called in Layout *before* the public-route early return (rules of hooks).

The old `/activity?tab=approvals` redirects to `/approvals?tab=held` (`retiredHubTab`). The
Releases page still shows its own Waiting-for-you cards (ReleaseCard, WaitingRow, VHostRequests are
exported and reused). The promote dialog's sent screen says who approves where, and links an admin
who may decide from here straight to Approvals.
