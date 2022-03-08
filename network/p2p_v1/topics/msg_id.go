package topics

import (
	"bytes"
	"github.com/bloxapp/ssv/protocol"
	scrypto "github.com/bloxapp/ssv/utils/crypto"
	"github.com/libp2p/go-libp2p-core/peer"
	ps_pb "github.com/libp2p/go-libp2p-pubsub/pb"
	"go.uber.org/zap"
	"sync"
	"time"
)

const (
	// MsgIDEmptyMessage is the msg_id for empty messages
	MsgIDEmptyMessage = "invalid:empty"
	// MsgIDBadEncodedMessage is the msg_id for messages with invalid encoding
	MsgIDBadEncodedMessage = "invalid:encoding"
	// MsgIDError is the msg_id for messages that we can't create their msg_id
	MsgIDError = "invalid:msg_id_error"
	// MsgIDBadPeerID is the msg_id for messages w/o a valid sender
	MsgIDBadPeerID = "invalid:peer_id_error"
)

// SSVMsgID returns msg_id for the given message
func SSVMsgID(msg []byte) string {
	if len(msg) == 0 {
		return ""
	}
	// TODO: check performance
	h := scrypto.Sha256Hash(msg)
	return string(h[20:])
}

// MsgPeersResolver will resolve the sending peers of the given message
type MsgPeersResolver interface {
	GetPeers(msg []byte) []peer.ID
}

// MsgIDHandler stores msgIDs and the corresponding sender peer.ID
// it works in memory as this store is expected to be invoked a lot, adding msgID and peerID pairs for every message
// this uses to identify msg senders after validation
type MsgIDHandler interface {
	MsgPeersResolver

	MsgID() func(pmsg *ps_pb.Message) string
	GC()
}

// msgIDEntry is a wrapper object that includes the sending peers and timing for expiration
type msgIDEntry struct {
	peers []peer.ID
	t     time.Time
}

// msgIDHandler implements MsgIDHandler
type msgIDHandler struct {
	logger *zap.Logger
	ids    map[string]*msgIDEntry
	locker sync.Locker
	ttl    time.Duration
}

func newMsgIDHandler(logger *zap.Logger, ttl time.Duration) MsgIDHandler {
	return &msgIDHandler{
		logger: logger,
		ids:    make(map[string]*msgIDEntry),
		locker: &sync.Mutex{},
		ttl:    ttl,
	}
}

// MsgID returns the msg_id function that calculates msg_id based on it's content
func (store *msgIDHandler) MsgID() func(pmsg *ps_pb.Message) string {
	return func(pmsg *ps_pb.Message) string {
		logger := store.logger
		if len(pmsg.Data) == 0 {
			logger.Warn("empty message", zap.ByteString("pmsg.From", pmsg.GetFrom()),
				zap.ByteString("seq_no", pmsg.GetSeqno()))
			//return fmt.Sprintf("%s/%s", MsgIDEmptyMessage, pubsub.DefaultMsgIdFn(pmsg))
			return MsgIDEmptyMessage
		}
		pid, err := peer.IDFromBytes(pmsg.GetFrom())
		if err != nil {
			logger.Warn("could not convert sender to peer id",
				zap.ByteString("pmsg.From", pmsg.GetFrom()), zap.Error(err))
			return MsgIDBadPeerID
		}
		logger = logger.With(zap.String("from", pid.String()))
		ssvMsg := protocol.SSVMessage{}
		err = ssvMsg.Decode(pmsg.GetData())
		if err != nil {
			logger.Warn("invalid encoding", zap.ByteString("seq_no", pmsg.GetSeqno()))
			return MsgIDBadEncodedMessage
		}
		mid := SSVMsgID(ssvMsg.Data)
		if len(mid) == 0 {
			logger.Warn("could not create msg_id", zap.ByteString("seq_no", pmsg.GetSeqno()))
			return MsgIDError
		}
		store.add(mid, pid)
		logger.Debug("msg_id created", zap.String("value", mid))
		return mid
	}
}

// GetPeers returns the peers that are related to the given msg
func (store *msgIDHandler) GetPeers(msg []byte) []peer.ID {
	msgID := SSVMsgID(msg)
	store.locker.Lock()
	defer store.locker.Unlock()
	entry, ok := store.ids[msgID]
	if ok {
		if !entry.t.Add(store.ttl).After(time.Now()) {
			return entry.peers
		}
		// otherwise -> expired
		delete(store.ids, msgID)
	}
	return []peer.ID{}
}

// add the pair of msg id and peer id
func (store *msgIDHandler) add(msgID string, pi peer.ID) {
	store.locker.Lock()
	defer store.locker.Unlock()

	entry, ok := store.ids[msgID]
	if !ok {
		entry = &msgIDEntry{
			peers: []peer.ID{},
		}
	}
	// extend expiration
	entry.t = time.Now()
	b := []byte(pi)
	for _, p := range entry.peers {
		if bytes.Equal([]byte(p), b) {
			return
		}
	}
	entry.peers = append(entry.peers, pi)
}

// GC performs garbage collection on the given map
func (store *msgIDHandler) GC() {
	store.locker.Lock()
	defer store.locker.Unlock()

	ids := make(map[string]*msgIDEntry)
	for m, entry := range store.ids {
		if entry.t.Add(store.ttl).After(time.Now()) {
			ids[m] = entry
		}
	}
	store.ids = ids
}
