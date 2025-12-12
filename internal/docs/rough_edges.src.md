# Rough edges: API decisions to reconsider for v2

This file collects a list of API oversights or rough edges that we've uncovered
post v1.0.0, along with their current workarounds. These issues can't be
addressed without breaking backward compatibility, but we'll revisit them for
v2.

- `EventStore.Open` is unnecessary. This was an artifact of an earlier version
  of the SDK where event persistence and delivery were combined.
  
  **Workaround**: `Open` may be implemented as a no-op.

- `Event` need not have been exported: it's an implementation detail of the SSE
  and streamable transports. Also the 'Name' field is a misnomer: it should be
  'event'.

- Enforcing valid tool names: with
  [SEP-986](https://github.com/modelcontextprotocol/modelcontextprotocol/issues/986)
  landing after the SDK was at v1, we missed an opportunity to panic on invalid
  tool names. Instead, we have to simply produce an error log. In v2, we should
  panic.

- Inconsistent naming.
  - `ResourceUpdatedNotificationsParams` should probably have just been
    `ResourceUpdatedParams`, as we don't include the word 'notification' in
    other notification param types.
  - Similarly, `ProgressNotificationParams` should probably have been
    `ProgressParams`.

- `AudioContent.MarshalJSON` should have had a pointer receiver, to be
  consistent with other content types.

- `ClientCapabilities.Roots` should have been a distinguished struct pointer
  ([see #607](https://github.com/modelcontextprotocol/go-sdk/issues/607)).

  **Workaround**: use `ClientCapabilities.RootsV2`, which aligns with the
  semantics of other capability fields.

- Default capabilities should have been empty. Instead, servers default to
  advertising `logging`, and clients default to advertising `roots` with
  `listChanged: true`. This is confusing because a nil `Capabilities` field
  does not mean "no capabilities".

  **Workaround**: to advertise no capabilities, set
  `ServerOptions.Capabilities` or `ClientOptions.Capabilities` to an empty
  `&ServerCapabilities{}` or `&ClientCapabilities{}` respectively.
