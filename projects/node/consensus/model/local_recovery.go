package model

// LocalEVMRecoveryProvider reports an operational mining hold independently
// from canonical validity, chain currentness, and peer consensus evidence.
type LocalEVMRecoveryProvider interface {
	LocalEVMRecoveryStatus() (held bool, reason string)
}
