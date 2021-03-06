package node

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/harmony-one/bls/ffi/go/bls"
	"github.com/harmony-one/harmony/api/proto"
	proto_discovery "github.com/harmony-one/harmony/api/proto/discovery"
	proto_identity "github.com/harmony-one/harmony/api/proto/identity"
	proto_node "github.com/harmony-one/harmony/api/proto/node"
	"github.com/harmony-one/harmony/api/service"
	"github.com/harmony-one/harmony/core/types"
	"github.com/harmony-one/harmony/crypto/pki"
	"github.com/harmony-one/harmony/internal/utils"
	"github.com/harmony-one/harmony/p2p"
	"github.com/harmony-one/harmony/p2p/host"
)

const (
	// MaxNumberOfTransactionsPerBlock is the max number of transaction per a block.
	MaxNumberOfTransactionsPerBlock = 8000
)

// MaybeBroadcastAsValidator returns if the node is a validator node.
func (node *Node) MaybeBroadcastAsValidator(content []byte) {
	// TODO: this is tree-based broadcasting. this needs to be replaced by p2p gossiping.
	if node.SelfPeer.ValidatorID > 0 && node.SelfPeer.ValidatorID <= host.MaxBroadCast {
		go host.BroadcastMessageFromValidator(node.host, node.SelfPeer, node.Consensus.GetValidatorPeers(), content)
	}
}

// StreamHandler handles a new incoming network message.
func (node *Node) StreamHandler(s p2p.Stream) {
	defer s.Close()

	// Read p2p message payload
	content, err := p2p.ReadMessageContent(s)

	if err != nil {
		utils.GetLogInstance().Error("Read p2p data failed", "err", err, "node", node)
		return
	}

	node.messageHandler(content, "")
}

// ReceiveGroupMessage use libp2p pubsub mechanism to receive broadcast messages
func (node *Node) ReceiveGroupMessage() {
	ctx := context.Background()
	for {
		if node.groupReceiver == nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		msg, sender, err := node.groupReceiver.Receive(ctx)
		if sender != node.host.GetID() {
			//			utils.GetLogInstance().Info("[PUBSUB]", "received group msg", len(msg), "sender", sender)
			if err == nil {
				// skip the first 5 bytes, 1 byte is p2p type, 4 bytes are message size
				node.messageHandler(msg[5:], string(sender))
			}
		}
	}
}

// messageHandler parses the message and dispatch the actions
func (node *Node) messageHandler(content []byte, sender string) {
	//	node.MaybeBroadcastAsValidator(content)

	consensusObj := node.Consensus

	msgCategory, err := proto.GetMessageCategory(content)
	if err != nil {
		utils.GetLogInstance().Error("Read node type failed", "err", err, "node", node)
		return
	}

	msgType, err := proto.GetMessageType(content)
	if err != nil {
		utils.GetLogInstance().Error("Read action type failed", "err", err, "node", node)
		return
	}

	msgPayload, err := proto.GetMessagePayload(content)
	if err != nil {
		utils.GetLogInstance().Error("Read message payload failed", "err", err, "node", node)
		return
	}

	switch msgCategory {
	case proto.Identity:
		actionType := proto_identity.IDMessageType(msgType)
		switch actionType {
		case proto_identity.Identity:
			messageType := proto_identity.MessageType(msgPayload[0])
			switch messageType {
			case proto_identity.Register:
				fmt.Println("received a identity message")
				utils.GetLogInstance().Info("NET: received message: IDENTITY/REGISTER")
			default:
				utils.GetLogInstance().Error("Announce message should be sent to IdentityChain")
			}
		}
	case proto.Consensus:
		msgPayload, _ := proto.GetConsensusMessagePayload(content)
		if consensusObj.IsLeader {
			utils.GetLogInstance().Info("NET: Leader received message:", "messageCategory", msgCategory, "messageType", msgType)
			consensusObj.ProcessMessageLeader(msgPayload)
		} else {
			utils.GetLogInstance().Info("NET: Validator received message:", "messageCategory", msgCategory, "messageType", msgType)
			consensusObj.ProcessMessageValidator(msgPayload)
			// TODO(minhdoan): add logic to check if the current blockchain is not sync with other consensus
			// we should switch to other state rather than DoingConsensus.
		}
	case proto.DRand:
		msgPayload, _ := proto.GetDRandMessagePayload(content)
		if node.DRand != nil {
			if node.DRand.IsLeader {
				utils.GetLogInstance().Info("NET: DRand Leader received message:", "messageCategory", msgCategory, "messageType", msgType)
				node.DRand.ProcessMessageLeader(msgPayload)
			} else {
				utils.GetLogInstance().Info("NET: DRand Validator received message:", "messageCategory", msgCategory, "messageType", msgType)
				node.DRand.ProcessMessageValidator(msgPayload)
			}
		}
	case proto.Node:
		actionType := proto_node.MessageType(msgType)
		switch actionType {
		case proto_node.Transaction:
			utils.GetLogInstance().Info("NET: received message: Node/Transaction")
			node.transactionMessageHandler(msgPayload)
		case proto_node.Block:
			utils.GetLogInstance().Info("NET: received message: Node/Block")
			blockMsgType := proto_node.BlockMessageType(msgPayload[0])
			switch blockMsgType {
			case proto_node.Sync:
				var blocks []*types.Block
				err := rlp.DecodeBytes(msgPayload[1:], &blocks) // skip the Sync messge type
				if err != nil {
					utils.GetLogInstance().Error("block sync", "error", err)
				} else {
					if node.Client != nil && node.Client.UpdateBlocks != nil && blocks != nil {
						node.Client.UpdateBlocks(blocks)
					}
				}
			}
		case proto_node.Control:
			utils.GetLogInstance().Info("NET: received message: Node/Control")
			controlType := msgPayload[0]
			if proto_node.ControlMessageType(controlType) == proto_node.STOP {
				utils.GetLogInstance().Debug("Stopping Node", "node", node, "numBlocks", node.blockchain.CurrentBlock().NumberU64(), "numTxsProcessed", node.countNumTransactionsInBlockchain())

				var avgBlockSizeInBytes common.StorageSize
				txCount := 0
				blockCount := 0
				avgTxSize := 0

				for block := node.blockchain.CurrentBlock(); block != nil; block = node.blockchain.GetBlockByHash(block.Header().ParentHash) {
					avgBlockSizeInBytes += block.Size()
					txCount += len(block.Transactions())
					bytes, _ := rlp.EncodeToBytes(block.Transactions())
					avgTxSize += len(bytes)
					blockCount++
				}

				if blockCount != 0 && txCount != 0 {
					avgBlockSizeInBytes = avgBlockSizeInBytes / common.StorageSize(blockCount)
					avgTxSize = avgTxSize / txCount
				}

				utils.GetLogInstance().Debug("Blockchain Report", "totalNumBlocks", blockCount, "avgBlockSizeInCurrentEpoch", avgBlockSizeInBytes, "totalNumTxs", txCount, "avgTxSzieInCurrentEpoch", avgTxSize)

				os.Exit(0)
			}
		case proto_node.PING:
			node.pingMessageHandler(msgPayload, sender)
		case proto_node.PONG:
			node.pongMessageHandler(msgPayload)
		}
	default:
		utils.GetLogInstance().Error("Unknown", "MsgCategory", msgCategory)
	}
}

func (node *Node) transactionMessageHandler(msgPayload []byte) {
	txMessageType := proto_node.TransactionMessageType(msgPayload[0])

	switch txMessageType {
	case proto_node.Send:
		txs := types.Transactions{}
		err := rlp.Decode(bytes.NewReader(msgPayload[1:]), &txs) // skip the Send messge type
		if err != nil {
			utils.GetLogInstance().Error("Failed to deserialize transaction list", "error", err)
		}
		node.addPendingTransactions(txs)

	case proto_node.Request:
		reader := bytes.NewBuffer(msgPayload[1:])
		txIDs := make(map[[32]byte]bool)
		buf := make([]byte, 32) // 32 byte hash Id
		for {
			_, err := reader.Read(buf)
			if err != nil {
				break
			}

			var txID [32]byte
			copy(txID[:], buf)
			txIDs[txID] = true
		}

		var txToReturn []*types.Transaction
		for _, tx := range node.pendingTransactions {
			if txIDs[tx.Hash()] {
				txToReturn = append(txToReturn, tx)
			}
		}
	}
}

// BroadcastNewBlock is called by consensus leader to sync new blocks with other clients/nodes.
// NOTE: For now, just send to the client (basically not broadcasting)
// TODO (lc): broadcast the new blocks to new nodes doing state sync
func (node *Node) BroadcastNewBlock(newBlock *types.Block) {
	if node.ClientPeer != nil {
		utils.GetLogInstance().Debug("Sending new block to client", "client", node.ClientPeer)
		if utils.UseLibP2P {
			node.host.SendMessageToGroups([]p2p.GroupID{p2p.GroupIDBeacon}, proto_node.ConstructBlocksSyncMessage([]*types.Block{newBlock}))
		} else {
			node.SendMessage(*node.ClientPeer, proto_node.ConstructBlocksSyncMessage([]*types.Block{newBlock}))
		}
	}
}

// VerifyNewBlock is called by consensus participants to verify the block (account model) they are running consensus on
func (node *Node) VerifyNewBlock(newBlock *types.Block) bool {
	err := node.blockchain.ValidateNewBlock(newBlock, pki.GetAddressFromPublicKey(node.SelfPeer.PubKey))
	if err != nil {
		utils.GetLogInstance().Debug("Failed verifying new block", "Error", err, "tx", newBlock.Transactions()[0])
		return false
	}

	err = node.blockchain.ValidateNewShardState(newBlock)
	if err != nil {
		utils.GetLogInstance().Debug("Failed to verify new sharding state", "err", err)
	}
	return true
}

// PostConsensusProcessing is called by consensus participants, after consensus is done, to:
// 1. add the new block to blockchain
// 2. [leader] send new block to the client
func (node *Node) PostConsensusProcessing(newBlock *types.Block) {
	if node.Role == BeaconLeader || node.Role == BeaconValidator {
		node.UpdateStakingList(newBlock)
	}
	if node.Consensus.IsLeader {
		node.BroadcastNewBlock(newBlock)
	}
	node.AddNewBlock(newBlock)

	// TODO: enable drand only for beacon chain
	if node.DRand != nil {
		go func() {
			node.ConfirmedBlockChannel <- newBlock
		}()
	}
}

// AddNewBlock is usedd to add new block into the blockchain.
func (node *Node) AddNewBlock(newBlock *types.Block) {
	blockNum, err := node.blockchain.InsertChain([]*types.Block{newBlock})

	if err != nil {
		utils.GetLogInstance().Debug("Error adding new block to blockchain", "blockNum", blockNum, "Error", err)
	} else {
		utils.GetLogInstance().Info("adding new block to blockchain", "blockNum", blockNum)
	}
}

func (node *Node) pingMessageHandler(msgPayload []byte, sender string) int {
	if sender != "" {
		_, ok := node.duplicatedPing[sender]
		if !ok {
			node.duplicatedPing[sender] = true
		} else {
			// duplicated ping message return
			return 0
		}
	}

	ping, err := proto_discovery.GetPingMessage(msgPayload)
	if err != nil {
		utils.GetLogInstance().Error("Can't get Ping Message")
		return -1
	}

	peer := new(p2p.Peer)
	peer.IP = ping.Node.IP
	peer.Port = ping.Node.Port
	peer.PeerID = ping.Node.PeerID
	peer.ValidatorID = ping.Node.ValidatorID

	peer.PubKey = &bls.PublicKey{}
	err = peer.PubKey.Deserialize(ping.Node.PubKey[:])
	if err != nil {
		utils.GetLogInstance().Error("UnmarshalBinary Failed", "error", err)
		return -1
	}

	//	utils.GetLogInstance().Debug("[pingMessageHandler]", "incoming peer", peer)

	// add to incoming peer list
	node.host.AddIncomingPeer(*peer)
	if utils.UseLibP2P {
		node.host.ConnectHostPeer(*peer)
	}

	if ping.Node.Role == proto_node.ClientRole {
		utils.GetLogInstance().Info("Add Client Peer to Node", "Node", node.Consensus.GetNodeID(), "Client", peer)
		node.ClientPeer = peer
		return 0
	}

	// Add to Node's peer list anyway
	node.AddPeers([]*p2p.Peer{peer})

	// This is the old way of broadcasting pong message
	if node.Consensus.IsLeader && !utils.UseLibP2P {
		peers := node.Consensus.GetValidatorPeers()
		pong := proto_discovery.NewPongMessage(peers, node.Consensus.PublicKeys)
		buffer := pong.ConstructPongMessage()

		// Send a Pong message directly to the sender
		// This is necessary because the sender will need to get a ValidatorID
		// Just broadcast won't work, some validators won't receive the latest
		// PublicKeys as we rely on a valid ValidatorID to do broadcast.
		// This is very buggy, but we will move to libp2p, hope the problem will
		// be resolved then.
		// However, I disable it for now as we are sending redundant PONG messages
		// to all validators.  This may not be needed. But it maybe add back.
		//   p2p.SendMessage(*peer, buffer)

		// Broadcast the message to all validators, as publicKeys is updated
		// FIXME: HAR-89 use a separate nodefind/neighbor message

		host.BroadcastMessageFromLeader(node.GetHost(), peers, buffer, node.Consensus.OfflinePeers)
		//		utils.GetLogInstance().Info("PingMsgHandler send pong message")
	}

	return 1
}

// SendPongMessage is the a goroutine to periodcally send pong message to all peers
func (node *Node) SendPongMessage() {
	tick := time.NewTicker(10 * time.Second)
	numPeers := len(node.Consensus.GetValidatorPeers())
	numPubKeys := len(node.Consensus.PublicKeys)
	sentMessage := false

	// Send Pong Message only when there is change on the number of peers
	for {
		select {
		case <-tick.C:
			peers := node.Consensus.GetValidatorPeers()
			numPeersNow := len(peers)
			numPubKeysNow := len(node.Consensus.PublicKeys)

			// no peers, wait for another tick
			if numPeersNow == 0 || numPubKeysNow == 0 {
				continue
			}
			// new peers added
			if numPubKeysNow != numPubKeys || numPeersNow != numPeers {
				sentMessage = false
			} else {
				// stable number of peers/pubkeys, sent the pong message
				if !sentMessage {
					pong := proto_discovery.NewPongMessage(peers, node.Consensus.PublicKeys)
					buffer := pong.ConstructPongMessage()
					content := host.ConstructP2pMessage(byte(0), buffer)
					err := node.host.SendMessageToGroups([]p2p.GroupID{p2p.GroupIDBeacon}, content)
					if err != nil {
						utils.GetLogInstance().Error("[PONG] failed to send pong message", "group", p2p.GroupIDBeacon)
						continue
					} else {
						utils.GetLogInstance().Info("[PONG] sent pong message to", "group", p2p.GroupIDBeacon)
					}
					sentMessage = true
					// stop sending ping message
					node.serviceManager.TakeAction(&service.Action{Action: service.Stop, ServiceType: service.PeerDiscovery})
				}
			}
			numPeers = numPeersNow
			numPubKeys = numPubKeysNow
		}
	}
}

func (node *Node) pongMessageHandler(msgPayload []byte) int {
	pong, err := proto_discovery.GetPongMessage(msgPayload)
	if err != nil {
		utils.GetLogInstance().Error("Can't get Pong Message")
		return -1
	}

	peers := make([]*p2p.Peer, 0)

	for _, p := range pong.Peers {
		peer := new(p2p.Peer)
		peer.IP = p.IP
		peer.Port = p.Port
		peer.ValidatorID = p.ValidatorID
		peer.PeerID = p.PeerID

		peer.PubKey = &bls.PublicKey{}
		err = peer.PubKey.Deserialize(p.PubKey[:])
		if err != nil {
			utils.GetLogInstance().Error("UnmarshalBinary Failed", "error", err)
			continue
		}
		peers = append(peers, peer)
	}

	if len(peers) > 0 {
		node.AddPeers(peers)
	}

	// Reset Validator PublicKeys every time we receive PONG message from Leader
	// The PublicKeys has to be idential across the shard on every node
	// TODO (lc): we need to handle RemovePeer situation
	publicKeys := make([]*bls.PublicKey, 0)

	// Create the the PubKey from the []byte sent from leader
	for _, k := range pong.PubKeys {
		key := bls.PublicKey{}
		err = key.Deserialize(k[:])
		if err != nil {
			utils.GetLogInstance().Error("UnmarshalBinary Failed PubKeys", "error", err)
			continue
		}
		publicKeys = append(publicKeys, &key)
	}

	utils.GetLogInstance().Debug("[pongMessageHandler]", "#keys", len(publicKeys), "#peers", len(peers))

	if node.State == NodeWaitToJoin {
		node.State = NodeReadyForConsensus
		// Notify JoinShard to stop sending Ping messages
		if node.StopPing != nil {
			node.StopPing <- struct{}{}
		}
	}

	// Stop discovery service after received pong message
	node.serviceManager.TakeAction(&service.Action{Action: service.Stop, ServiceType: service.PeerDiscovery})
	return node.Consensus.UpdatePublicKeys(publicKeys)
}
