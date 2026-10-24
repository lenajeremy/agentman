package daemon

import (
	"context"
	"sync"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// requestDispatch routes one app connection's requests to the handler, by the
// rules every connection follows, whichever way the app reached the daemon:
//
//   - subscribe and unsubscribe are connection state, applied synchronously
//     and in arrival order;
//   - actions on a session (queuedInOrder) run one at a time, in arrival
//     order, on a single consumer;
//   - everything else, mostly history and workspace reads, runs concurrently,
//     up to maxConcurrentRequests in flight.
//
// The relay connection and the local socket each make one per connection, so
// the two can never order a user's taps differently.
type requestDispatch struct {
	ctx      context.Context
	handler  func(context.Context, string, protocol.Request) protocol.Event
	reply    func(replyTo string, event protocol.Event)
	inFlight chan struct{}
	ordered  chan daemonRequestJob
	wg       sync.WaitGroup
}

func newRequestDispatch(
	ctx context.Context,
	handler func(context.Context, string, protocol.Request) protocol.Event,
	reply func(replyTo string, event protocol.Event),
) *requestDispatch {
	d := &requestDispatch{
		ctx:      ctx,
		handler:  handler,
		reply:    reply,
		inFlight: make(chan struct{}, maxConcurrentRequests),
		ordered:  make(chan daemonRequestJob, maxConcurrentRequests),
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		runOrderedMutationQueue(ctx, d.ordered, handler, reply, func() { <-d.inFlight })
	}()
	return d
}

// dispatch handles one request from deviceID, replying to replyTo.
func (d *requestDispatch) dispatch(deviceID, replyTo string, req protocol.Request) {
	// Subscription ownership is connection state, not background work. Apply
	// it synchronously so subscribe -> unsubscribe can never be reversed by
	// the scheduler and leave a transcript follower running after navigation.
	if req.Type == protocol.ReqSubscribe || req.Type == protocol.ReqUnsubscribe {
		if event := d.handler(d.ctx, deviceID, req); event.Type != "" {
			d.reply(replyTo, event)
		}
		return
	}

	// History reads remain concurrent, while terminal mutations enter one
	// arrival-ordered queue. A mutex alone only prevents overlap; it does not
	// promise FIFO and could send two rapid messages in reverse order.
	select {
	case d.inFlight <- struct{}{}:
	default:
		d.reply(replyTo, protocol.Event{
			Type: protocol.EvtError, Error: "daemon is handling too many requests",
		})
		return
	}
	if queuedInOrder(req.Type) {
		d.ordered <- daemonRequestJob{req: req, replyTo: replyTo, deviceID: deviceID}
		return
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer func() { <-d.inFlight }()
		event := d.handler(d.ctx, deviceID, req)
		if event.Type == "" {
			return // acknowledged with nothing to say (subscribe/unsubscribe)
		}
		d.reply(replyTo, event)
	}()
}

// close stops taking requests and waits for the ones in flight. The context
// given to newRequestDispatch must already be cancelled or about to be, or a
// slow handler keeps this waiting.
func (d *requestDispatch) close() {
	close(d.ordered)
	d.wg.Wait()
}
