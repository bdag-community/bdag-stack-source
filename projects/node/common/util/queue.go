package util

type IterativeQueue []any

func NewIterativeQueue() IterativeQueue {
	iq := make(IterativeQueue, 0)
	return iq
}

func (iq *IterativeQueue) Enqueue(item any) {
	*iq = append(*iq, item)
}

func (iq *IterativeQueue) Peek() any {
	return (*iq)[0]
}

func (iq *IterativeQueue) Dequeue() any {
	first := (*iq)[0]
	*iq = (*iq)[1:]
	return first
}

func (iq *IterativeQueue) Len() int {
	return len(*iq)
}
