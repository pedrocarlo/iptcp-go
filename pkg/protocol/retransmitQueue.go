package protocol

import "time"

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
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	old[n-1] = nil
	return x
}

func (h rtoQueue) Peek() *rtoEntry {
	return h[h.Len()-1]
}

func (h rtoQueue) Cleanup(tcb *TCB) []*rtoEntry {
	popped := make([]*rtoEntry, 0)
	for h.Len() != 0 {
		// Already acknowledged data
		if wrappedCompare(h.Peek().seqNum, tcb.wrapFromIss(tcb.sendUna)) == -1 {
			// popped = append(popped, h.Pop().(*rtoEntry))
			h.Pop()
		}
	}
	return popped
}

func (h rtoQueue) Items() []*rtoEntry {
	return h
}
