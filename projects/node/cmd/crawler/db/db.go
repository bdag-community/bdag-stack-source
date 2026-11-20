package db

import (
	"encoding/json"
	"log"

	"github.com/BlockdagNetworkLabs/bdag/cmd/crawler/db/base"
)

const (
	peer_Bucket = "peer_Bucket"
)

type Peer struct {
	Id          string
	Addr        string
	ConnectTime int64
	Connected   bool
}

type PeerDB struct {
	base *base.Base
}

func OpenPeerDB(path string) (*PeerDB, error) {
	b, err := base.Open(path)
	if err != nil {
		return nil, err
	}
	return &PeerDB{
		base: b,
	}, nil
}

func (p *PeerDB) Close() error {
	return p.base.Close()
}

func (p *PeerDB) PeerList() []*Peer {
	bytesList := p.base.Foreach(peer_Bucket)
	peerList := []*Peer{}
	for _, bytes := range bytesList {
		peerInfo := &Peer{}
		if err := json.Unmarshal(bytes, peerInfo); err != nil {
			log.Printf("failed to unmarshal peer info: %v", err)
			continue
		}

		peerList = append(peerList, peerInfo)
	}
	return peerList
}

func (p *PeerDB) UpdatePeer(peerInfo *Peer) {
	bytes, _ := json.Marshal(peerInfo)
	p.base.PutInBucket(peer_Bucket, []byte(peerInfo.Id), bytes)
}
