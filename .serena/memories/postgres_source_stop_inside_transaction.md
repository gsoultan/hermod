# Postgres source: a stop inside a transaction

`pkg/comm/source/postgres/postgres.go`: `receiveOpenCommit`, `mayFinishOpenTxn`, `emit`.

## The rule Postgres applies

On `START_REPLICATION` the server replays every transaction whose commit lies beyond the slot's
confirmed position. It decides per transaction, by the commit, never per row. pgoutput sends a
transaction as Begin, its rows, then Commit, all at once when the transaction commits.

## The gap

The source hands a row to the pipeline the moment it arrives. The row can be written and
acknowledged, and the engine can close the source, while the Commit is still unread in the socket.
`flushPosition` then returns the row's position, which is before the commit, so the next source on
the slot is handed the whole transaction again. Every row of it had been acknowledged.

In CI it showed as `TestCloseConfirmsTheAcknowledgedPosition` failing about once in several
hundred runs with "the next source started with id 5 again".

## What the stop does now

`streamLoop` calls `receiveOpenCommit` before `confirmFinalPosition`. If the stream is between a
Begin and its Commit (`inTxn`), it keeps receiving for at most `finalCommitWait` (1s) until the
Commit, which moves `lastReceivedLSN` past the transaction. `flushPosition` still confirms the
received position only when nothing delivered is unacknowledged, so this cannot confirm past work
in flight.

It must not read on in two cases, because confirming the commit would discard a row nobody has:

- a row of the transaction was received and not handed over (`txnRowDropped`, set in `emit` when
  `dispatch` returns an error; `dispatch` can pick `ctx.Done()` even when the channel has room);
- another row arrives while reading on: there is no one left to hand it to, so it returns and the
  transaction is replayed whole on the next start.

`inTxn` and `txnRowDropped` belong to the stream goroutine and are reset when a stream starts.

## Tests

- `close_between_row_and_commit_integration_test.go` (real Postgres, `-tags=integration`): the
  `afterEmit` hook parks the stream goroutine after the nth row until the stop, so the stop lands
  in the gap every time. One test for "acknowledged row is not replayed", one for "a transaction
  with an undelivered row is replayed whole". Removing either half of the fix fails one of them.
- `open_txn_test.go`: a dropped row makes the transaction unfinishable.

`afterEmit` is nil in production.
