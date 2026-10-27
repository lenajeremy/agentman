package daemon

import (
	"context"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

const (
	// maxQueuedActions bounds the actions (messages, answers, switches)
	// waiting their turn on one connection, apart from the reads: a backlog
	// of sends, each fetching its pictures from the relay, once took every
	// slot and left the phone unable even to list a folder.
	maxQueuedActions = 16
	// requestDeadline bounds any one request. The longest a request has
	// reason to take is writing out a long voice recording.
	requestDeadline = 3 * time.Minute
	// slowRequest is how long a request runs before the log says so, naming
	// it, so a daemon that seems stuck can be read rather than guessed at.
	slowRequest = 5 * time.Second
)

// Logf, when set, writes a line to the daemon's log (am serve sets it).
var Logf func(format string, args ...any)

// requestDispatch routes one app connection's requests to the handler, by the
// rules every connection follows, whichever way the app reached the daemon:
//
//   - subscribe and unsubscribe are connection state, applied synchronously
//     and in arrival order;
//   - actions on a session (queuedInOrder) run one at a time, in arrival
//     order, on a single consumer;
//   - everything else, mostly history and workspace reads, runs concurrently,
//     up to maxConcurrentRequests in flight, apart from the actions waiting,
//     so one stuck action never starves the reads;
//   - each runs to requestDeadline at most, and one past slowRequest is
//     logged.
//
// The relay connection and the local socket each make one per connection, so
// the two can never order a user's taps differently.
type requestDispatch struct {
	ctx      context.Context
	handler  func(context.Context, string, protocol.Request) protocol.Event
	reply    func(replyTo string, event protocol.Event)
	inFlight chan struct{}
	queued   chan struct{}
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
		queued:   make(chan struct{}, maxQueuedActions),
		ordered:  make(chan daemonRequestJob, maxQueuedActions),
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		runOrderedMutationQueue(ctx, d.ordered, d.handle, reply, func() { <-d.queued })
	}()
	return d
}

// handle runs one request to its deadline, and logs it when it is slow.
func (d *requestDispatch) handle(ctx context.Context, deviceID string, req protocol.Request) protocol.Event {
	ctx, cancel := context.WithTimeout(ctx, requestDeadline)
	defer cancel()
	started := time.Now()
	event := d.handler(ctx, deviceID, req)
	if took := time.Since(started); took >= slowRequest && Logf != nil {
		session := req.SessionID
		if len(session) > 20 {
			session = session[:20] + "…"
		}
		Logf("slow       %s %s took %s", req.Type, session, took.Round(100*time.Millisecond))
	}
	return event
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
	if queuedInOrder(req.Type) {
		select {
		case d.queued <- struct{}{}:
		default:
			d.reply(replyTo, protocol.Event{
				Type: protocol.EvtError, SessionID: req.SessionID,
				Error: "daemon: too many messages are waiting to reach the agent; try again in a moment",
			})
			return
		}
		d.ordered <- daemonRequestJob{req: req, replyTo: replyTo, deviceID: deviceID}
		return
	}
	select {
	case d.inFlight <- struct{}{}:
	default:
		d.reply(replyTo, protocol.Event{
			Type: protocol.EvtError, Error: "daemon is handling too many requests",
		})
		return
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer func() { <-d.inFlight }()
		event := d.handle(d.ctx, deviceID, req)
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
