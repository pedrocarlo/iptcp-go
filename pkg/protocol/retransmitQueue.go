package protocol

import (
	"time"
)

type rtoEntry struct {
	seqNum  uint32
	flags   uint8
	payload []byte
	start   time.Time
}

type rtoQueue []*rtoEntry

func (h rtoQueue) Len() int {
	return len(h)
}

// Should not have duplicate entries in queue
func (h rtoQueue) Less(i, j int) bool {
	entry1, entry2 := h[i], h[j]
	if entry1.seqNum-entry2.seqNum > uint32(tcbSize) {
		return false // entry 1 is bigger
	} else if entry1.seqNum < entry2.seqNum {
		return true // entry 1 is less than
	}
	return true // Should never happen
}

func (h rtoQueue) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *rtoQueue) Push(x interface{}) {
	*h = append(*h, x.(*rtoEntry))
}

func (h *rtoQueue) Pop() interface{} {
	if h.Len() <= 0 {
		return nil
	}
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

func (h *rtoQueue) Peek() *rtoEntry {
	if h.Len() > 0 {
		return (*h)[h.Len()-1]
	}
	return nil
}

func (h *rtoQueue) Cleanup(tcb *TCB) []*rtoEntry {
	popped := make([]*rtoEntry, 0)
	for curr := h.Peek(); curr != nil; curr = h.Peek() {
		// Already acknowledged data
		// println("Seq Num entry:", curr.seqNum)
		// println("send una wrapped:", tcb.wrapFromIss(tcb.sendUna), "sendUna normal", tcb.sendUna)
		// println("compare:", wrappedCompare(curr.seqNum, tcb.wrapFromIss(tcb.sendUna)))
		if wrappedCompare(curr.seqNum, tcb.wrapFromIss(tcb.sendUna)) >= 0 {
			break
		}
		popped = append(popped, h.Pop().(*rtoEntry))
	}
	return popped
}

func (h rtoQueue) Items() []*rtoEntry {
	return h
}
