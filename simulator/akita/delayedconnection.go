package akita

import (
	"strings"
)

// pendingMsg wraps a message together with the simulation time at which it
// should be delivered after NUMA latency has elapsed.
type pendingMsg struct {
	msg         Msg
	deliverTime VTimeInSec
}

type delayConnectionEnd struct {
	port    Port
	buf     []Msg
	bufSize int
	busy    bool

	// pendingBuf holds messages that have been dequeued from buf and are
	// waiting for their NUMA latency to expire before final delivery.
	pendingBuf []pendingMsg
}

// DelayedConnection connects two components, optionally adding NUMA latency.
type DelayedConnection struct {
	*TickingComponent

	engine     Engine
	nextPortID int
	ports      []Port
	ends       map[Port]*delayConnectionEnd

	L2Port Port

	// GetLatency is an optional callback invoked for every outgoing message.
	// It should return the extra NUMA latency (in seconds) that must elapse
	// before the message is delivered to its destination port.
	// When GetLatency is nil the original zero-latency behaviour is preserved.
	GetLatency func(msg Msg) VTimeInSec
}

// PlugIn marks the port connects to this DelayedConnection.
func (c *DelayedConnection) PlugIn(port Port, sourceSideBufSize int) {
	c.Lock()
	defer c.Unlock()

	c.ports = append(c.ports, port)
	end := &delayConnectionEnd{}
	end.port = port
	end.bufSize = sourceSideBufSize
	c.ends[port] = end

	if strings.Contains(port.Name(), "L2TLB.TopPort") {
		c.L2Port = port
	}

	port.SetConnection(c)
}

// Unplug marks the port no longer connects to this DelayedConnection.
func (c *DelayedConnection) Unplug(port Port) {
	panic("not implemented")
}

// NotifyAvailable is called by a port to notify that the connection can
// deliver to the port again.
func (c *DelayedConnection) NotifyAvailable(now VTimeInSec, port Port) {
	c.TickNow(now)
}

// Send of a DelayedConnection schedules a DeliveryEvent immediately
func (c *DelayedConnection) Send(msg Msg) *SendError {
	c.Lock()
	defer c.Unlock()

	c.msgMustBeValid(msg)

	var srcEnd *delayConnectionEnd

	if strings.Contains(c.Name(), "L1TLB-L2TLB") {
		msgMeta := msg.Meta()
		// srcName := msgMeta.Src.Name()
		if msgMeta.PutInL2TLBBuffer {
			// if srcName == msgMeta.Dst.Name() && strings.Contains(srcName, "RTU") {
			srcEnd = c.ends[c.L2Port]
		} else {
			srcEnd = c.ends[msgMeta.Src]
		}
	} else {
		srcEnd = c.ends[msg.Meta().Src]
	}

	if len(srcEnd.buf) >= srcEnd.bufSize {
		srcEnd.busy = true
		return NewSendError()
	}

	srcEnd.buf = append(srcEnd.buf, msg)

	c.TickNow(msg.Meta().SendTime)

	return nil
}

func (c *DelayedConnection) msgMustBeValid(msg Msg) {
	c.portMustNotBeNil(msg.Meta().Src)
	c.portMustNotBeNil(msg.Meta().Dst)
	c.portMustBeConnected(msg.Meta().Src)
	c.portMustBeConnected(msg.Meta().Dst)
	c.srcDstMustNotBeTheSame(msg)
}

func (c *DelayedConnection) portMustNotBeNil(port Port) {
	if port == nil {
		panic("src or dst is not given")
	}
}

func (c *DelayedConnection) portMustBeConnected(port Port) {
	if _, connected := c.ends[port]; !connected {
		panic("src or dst is not connected")
	}
}

func (c *DelayedConnection) srcDstMustNotBeTheSame(msg Msg) {
	if msg.Meta().Src == msg.Meta().Dst && !strings.Contains(msg.Meta().Src.Name(), "RTU") {
		panic("sending back to src")
	}
}

func (c *DelayedConnection) Tick(now VTimeInSec) bool {
	madeProgress := false
	for i := 0; i < len(c.ports); i++ {
		portID := (i + c.nextPortID) % len(c.ports)
		port := c.ports[portID]
		end := c.ends[port]
		// Deliver any pending messages whose NUMA latency has elapsed first,
		// then pull new messages from the source buffer.
		madeProgress = c.deliverPending(end, now) || madeProgress
		madeProgress = c.forwardMany(end, now) || madeProgress
	}
	c.nextPortID = (c.nextPortID + 1) % len(c.ports)
	return madeProgress
}

// forwardMany dequeues messages from end.buf.  When GetLatency is set it calls
// it to obtain the NUMA latency for each message and places the message into
// end.pendingBuf so that deliverPending can deliver it once the time is right.
// When GetLatency is nil the original zero-latency behaviour is preserved.
func (c *DelayedConnection) forwardMany(
	end *delayConnectionEnd,
	now VTimeInSec,
) bool {
	madeProgress := false
	for {
		if len(end.buf) == 0 {
			break
		}

		head := end.buf[0]

		if c.GetLatency != nil {
			// Obtain NUMA latency and defer delivery.
			latency := c.GetLatency(head)
			end.pendingBuf = append(end.pendingBuf, pendingMsg{
				msg:         head,
				deliverTime: now + latency,
			})
			end.buf = end.buf[1:]

			if end.busy {
				end.port.NotifyAvailable(now)
				end.busy = false
			}

			madeProgress = true
			continue
		}

		// No latency function — deliver immediately (original behaviour).
		head.Meta().RecvTime = now

		err := head.Meta().Dst.Recv(head)
		if err != nil {
			break
		}

		madeProgress = true
		end.buf = end.buf[1:]

		if end.busy {
			end.port.NotifyAvailable(now)
			end.busy = false
		}
	}

	return madeProgress
}

// deliverPending walks end.pendingBuf in FIFO order and delivers every message
// whose deliverTime has been reached.  It stops at the first message that is
// not yet due, or that the destination port cannot accept.
func (c *DelayedConnection) deliverPending(
	end *delayConnectionEnd,
	now VTimeInSec,
) bool {
	madeProgress := false
	w := 0 // write cursor for entries that must stay in pendingBuf
	for _, pm := range end.pendingBuf {
		if pm.deliverTime <= now {
			pm.msg.Meta().RecvTime = now
			err := pm.msg.Meta().Dst.Recv(pm.msg)
			if err == nil {
				// Successfully delivered - drop from pendingBuf.
				madeProgress = true

				if end.busy {
					end.port.NotifyAvailable(now)
					end.busy = false
				}

				continue
			}
			// Destination busy - keep the message so we retry next tick.
		}
		end.pendingBuf[w] = pm
		w++
	}
	end.pendingBuf = end.pendingBuf[:w]
	return madeProgress || len(end.pendingBuf) > 0
}

func (c *DelayedConnection) forwardOne(
	end *delayConnectionEnd,
	now VTimeInSec,
) bool {
	madeProgress := false
	if len(end.buf) == 0 {
		return madeProgress
	}

	head := end.buf[0]
	head.Meta().RecvTime = now

	err := head.Meta().Dst.Recv(head)
	if err != nil {
		return madeProgress
	}

	madeProgress = true
	end.buf = end.buf[1:]

	if end.busy {
		end.port.NotifyAvailable(now)
		end.busy = false
	}

	return madeProgress
}

// NewDelayedDirectConnection creates a new DelayedConnection object
func NewDelayedDirectConnection(
	name string,
	engine Engine,
	freq Freq,
	latency func(msg Msg) VTimeInSec,
) *DelayedConnection {
	c := new(DelayedConnection)
	c.TickingComponent = NewSecondaryTickingComponent(name, engine, freq, c)
	c.ends = make(map[Port]*delayConnectionEnd)
	c.GetLatency = latency
	return c
}
