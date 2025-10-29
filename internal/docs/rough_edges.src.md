# Rough edges: API decisions to reconsider for v2

This file collects a list of API oversights or rough edges that we've uncovered
post v1.0.0, along with their current workarounds. These issues can't be
addressed without breaking backward compatibility, but we'll revisit them for
v2.

- `EventStore.Open` is unnecessary. This was an artifact of an earlier version
  of the SDK where event persistence and delivery were combined.
  
  **Workaround**: `Open` may be implemented as a no-op.
