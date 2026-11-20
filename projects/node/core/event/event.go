package event

const (
	Initialized     = int(0x01)
	DownloaderStart = int(0x02)
	DownloaderEnd   = int(0x03)
)

type Event struct {
	Data any
	Ack  chan<- struct{}
}

func New(data any) *Event {
	return &Event{Data: data, Ack: nil}
}
