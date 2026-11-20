package rpc

import "testing"

func TestSubscriptionActivationSignalMatchesActualNotifierHandoff(t *testing.T) {
	n := &Notifier{active: make(map[ID]*Subscription), inactive: make(map[ID]*Subscription)}
	sub := n.CreateSubscription()
	select {
	case <-sub.Activated():
		t.Fatal("inactive subscription was admitted")
	default:
	}
	n.activate(sub.ID, "fixture")
	select {
	case <-sub.Activated():
	default:
		t.Fatal("active subscription sender remains blocked")
	}
	if n.active[sub.ID] != sub || n.active[sub.ID].namespace != "fixture" || n.inactive[sub.ID] != nil {
		t.Fatal("activation signal does not bind actual registration")
	}
	n.activate(sub.ID, "fixture") // A duplicate cannot close the signal twice.
}
