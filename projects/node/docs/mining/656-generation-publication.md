# Required GBT generation publication and recovery (#656)

Actual handler tests reproduced stale publication after both a pending required
rebuild and an asynchronous chain rebuild failed. The old template is retained
for submit classification; it must not be republished as current work.

This adapts the complete source-parent behavior from3fbcfddc/751f2cc9,
queued-sequence correctiona21603e9 and locked-worker correction2b70d6.
Required failures persist in a mutex-protected generation hold. Pending request
errors reach their actual caller. Benign mempool failure leaves the otherwise
valid generation available. No-pending requests and rejected optional PoW
requests retain existing behavior; a failed optional PoW change restores mode.

All private peer/backend/canonical-checkpoint/local-EVM guards apply before
replacement-build admission is copied. A generation failure then holds only
publication; it cannot relax any of those guards or prevent a healthy repair.
Validated replacement and authorization are installed before the hold clears
and before the actual first notification. The production consensus median-time
and authorization reader are used; no test-only bypass was added.

A synchronous getter passes exact locked-worker ownership only on its direct
call. Queued requests carry no worker lock. Other builds acquire their worker
mutex. Rebuild tests exercise actual native consensus/templates, both PoW and
pay-address branches, retained payout checks and lock release.

A monotonic sequence watermark survives replacement. Both subscription bridge
admission, eager snapshot and final latest-queue delivery refuse invalidated
payloads; the actual authenticated local WebSocket regression proves replayed
old payloads cannot become publishable after repair. Existing bounded latest-
only backpressure remains. This is admission revocation, not recall of frames
already delivered before failure.

The private getter has no derived serialized cache: every response serializes
current source anew. Core655fee identity, aggregate/rewards/schema/header/
consensus/authorization and decoder/security/SDK fixes are retained. Existing
ordinary/race fee vectors and actual JSON invariance protect that behavior.
Full current-Core, populated history/currentness/positiveR2/mining/network,
quality/security/license/protected review/final release remain separate gates.


The actual race-mode wire test also exposed the inherited eager-subscription
handoff defect: Notifier.Notify silently drops inactive frames. Subscription
now exposes an activation signal, closed only by the real notifier registration
after the response is sent. The mining sender waits on that signal or existing
close/error channels; its bridge continues draining. No arbitrary delay, extra
payload buffer, JSON field or authentication change was added.
