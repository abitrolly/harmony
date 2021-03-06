package node

import (
	"fmt"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/harmony-one/harmony/drand"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	proto_discovery "github.com/harmony-one/harmony/api/proto/discovery"
	"github.com/harmony-one/harmony/consensus"
	"github.com/harmony-one/harmony/core/types"
	"github.com/harmony-one/harmony/crypto/pki"
	"github.com/harmony-one/harmony/internal/utils"
	"github.com/harmony-one/harmony/p2p"
	"github.com/harmony-one/harmony/p2p/p2pimpl"
	"golang.org/x/crypto/sha3"
)

func TestNewNode(t *testing.T) {
	_, pubKey := utils.GenKey("1", "2")
	leader := p2p.Peer{IP: "127.0.0.1", Port: "8882", PubKey: pubKey}
	validator := p2p.Peer{IP: "127.0.0.1", Port: "8885"}
	priKey, _, _ := utils.GenKeyP2P("127.0.0.1", "9902")
	host, err := p2pimpl.NewHost(&leader, priKey)
	if err != nil {
		t.Fatalf("newhost failure: %v", err)
	}
	consensus := consensus.New(host, "0", []p2p.Peer{leader, validator}, leader)
	node := New(host, consensus, nil)
	if node.Consensus == nil {
		t.Error("Consensus is not initialized for the node")
	}

	if node.blockchain == nil {
		t.Error("Blockchain is not initialized for the node")
	}

	if node.blockchain.CurrentBlock() == nil {
		t.Error("Genesis block is not initialized for the node")
	}
}

func TestGetSyncingPeers(t *testing.T) {
	_, pubKey := utils.GenKey("1", "2")
	leader := p2p.Peer{IP: "127.0.0.1", Port: "8882", PubKey: pubKey}
	validator := p2p.Peer{IP: "127.0.0.1", Port: "8885"}
	priKey, _, _ := utils.GenKeyP2P("127.0.0.1", "9902")
	host, err := p2pimpl.NewHost(&leader, priKey)
	if err != nil {
		t.Fatalf("newhost failure: %v", err)
	}

	consensus := consensus.New(host, "0", []p2p.Peer{leader, validator}, leader)

	node := New(host, consensus, nil)
	peer := p2p.Peer{IP: "127.0.0.1", Port: "8000"}
	peer2 := p2p.Peer{IP: "127.0.0.1", Port: "8001"}
	node.Neighbors.Store("minh", peer)
	node.Neighbors.Store("mark", peer2)
	res := node.GetSyncingPeers()
	if len(res) == 0 || !(res[0].IP == peer.IP || res[0].IP == peer2.IP) {
		t.Error("GetSyncingPeers should return list of {peer, peer2}")
	}
	if len(res) == 0 || (res[0].Port != "5000" && res[0].Port != "5001") {
		t.Errorf("Syncing ports should be 5000, got %v", res[0].Port)
	}
}

func TestAddPeers(t *testing.T) {
	pubKey1 := pki.GetBLSPrivateKeyFromInt(333).GetPublicKey()
	pubKey2 := pki.GetBLSPrivateKeyFromInt(444).GetPublicKey()

	peers1 := []*p2p.Peer{
		&p2p.Peer{
			IP:          "127.0.0.1",
			Port:        "8888",
			PubKey:      pubKey1,
			ValidatorID: 1,
		},
		&p2p.Peer{
			IP:          "127.0.0.1",
			Port:        "9999",
			PubKey:      pubKey2,
			ValidatorID: 2,
		},
	}
	_, pubKey := utils.GenKey("1", "2")
	leader := p2p.Peer{IP: "127.0.0.1", Port: "8982", PubKey: pubKey}
	validator := p2p.Peer{IP: "127.0.0.1", Port: "8985"}
	priKey, _, _ := utils.GenKeyP2P("127.0.0.1", "9902")
	host, err := p2pimpl.NewHost(&leader, priKey)
	if err != nil {
		t.Fatalf("newhost failure: %v", err)
	}
	consensus := consensus.New(host, "0", []p2p.Peer{leader, validator}, leader)
	dRand := drand.New(host, "0", []p2p.Peer{leader, validator}, leader, nil)

	node := New(host, consensus, nil)
	node.DRand = dRand
	r1 := node.AddPeers(peers1)
	e1 := 2
	if r1 != e1 {
		t.Errorf("Add %v peers, expectd %v", r1, e1)
	}
	r2 := node.AddPeers(peers1)
	e2 := 0
	if r2 != e2 {
		t.Errorf("Add %v peers, expectd %v", r2, e2)
	}
}

func sendPingMessage(node *Node, leader p2p.Peer) {
	pubKey1 := pki.GetBLSPrivateKeyFromInt(333).GetPublicKey()

	p1 := p2p.Peer{
		IP:     "127.0.0.1",
		Port:   "9999",
		PubKey: pubKey1,
	}

	ping1 := proto_discovery.NewPingMessage(p1)
	buf1 := ping1.ConstructPingMessage()

	fmt.Println("waiting for 5 seconds ...")
	time.Sleep(5 * time.Second)

	node.SendMessage(leader, buf1)
	fmt.Println("sent ping message ...")
}

func sendPongMessage(node *Node, leader p2p.Peer) {
	pubKey1 := pki.GetBLSPrivateKeyFromInt(333).GetPublicKey()
	pubKey2 := pki.GetBLSPrivateKeyFromInt(444).GetPublicKey()
	p1 := p2p.Peer{
		IP:     "127.0.0.1",
		Port:   "9998",
		PubKey: pubKey1,
	}
	p2 := p2p.Peer{
		IP:     "127.0.0.1",
		Port:   "9999",
		PubKey: pubKey2,
	}

	pong1 := proto_discovery.NewPongMessage([]p2p.Peer{p1, p2}, nil)
	buf1 := pong1.ConstructPongMessage()

	fmt.Println("waiting for 10 seconds ...")
	time.Sleep(10 * time.Second)

	node.SendMessage(leader, buf1)
	fmt.Println("sent pong message ...")
}

func exitServer() {
	fmt.Println("wait 5 seconds to terminate the process ...")
	time.Sleep(5 * time.Second)

	os.Exit(0)
}

func TestPingPongHandler(t *testing.T) {
	_, pubKey := utils.GenKey("127.0.0.1", "8881")
	leader := p2p.Peer{IP: "127.0.0.1", Port: "8881", PubKey: pubKey}
	//   validator := p2p.Peer{IP: "127.0.0.1", Port: "9991"}
	priKey, _, _ := utils.GenKeyP2P("127.0.0.1", "9902")
	host, err := p2pimpl.NewHost(&leader, priKey)
	if err != nil {
		t.Fatalf("newhost failure: %v", err)
	}
	consensus := consensus.New(host, "0", []p2p.Peer{leader}, leader)
	node := New(host, consensus, nil)
	//go sendPingMessage(leader)
	go sendPongMessage(node, leader)
	go exitServer()
	node.StartServer()
}

func TestUpdateStakingDeposit(t *testing.T) {
	_, pubKey := utils.GenKey("1", "2")
	leader := p2p.Peer{IP: "127.0.0.1", Port: "8882", PubKey: pubKey}
	validator := p2p.Peer{IP: "127.0.0.1", Port: "8885"}
	priKey, _, _ := utils.GenKeyP2P("127.0.0.1", "9902")
	host, err := p2pimpl.NewHost(&leader, priKey)
	if err != nil {
		t.Fatalf("newhost failure: %v", err)
	}
	consensus := consensus.New(host, "0", []p2p.Peer{leader, validator}, leader)

	node := New(host, consensus, nil)
	node.CurrentStakes = make(map[common.Address]int64)

	DepositContractPriKey, _ := crypto.GenerateKey()                                  //DepositContractPriKey is pk for contract
	DepositContractAddress := crypto.PubkeyToAddress(DepositContractPriKey.PublicKey) //DepositContractAddress is the address for the contract
	node.StakingContractAddress = DepositContractAddress
	node.AccountKey, _ = crypto.GenerateKey()
	Address := crypto.PubkeyToAddress(node.AccountKey.PublicKey)
	callingFunction := "0xd0e30db0"
	amount := new(big.Int)
	amount.SetString("10", 10)
	dataEnc := common.FromHex(callingFunction) //Deposit Does not take a argument, stake is transferred via amount.
	tx1, err := types.SignTx(types.NewTransaction(0, DepositContractAddress, node.Consensus.ShardID, amount, params.TxGasContractCreation*10, nil, dataEnc), types.HomesteadSigner{}, node.AccountKey)

	var txs []*types.Transaction
	txs = append(txs, tx1)
	header := &types.Header{Extra: []byte("hello")}
	block := types.NewBlock(header, txs, nil)
	node.UpdateStakingList(block)
	if len(node.CurrentStakes) == 0 {
		t.Error("New node's stake was not added")
	}
	value, ok := node.CurrentStakes[Address]
	if !ok {
		t.Error("The correct address was not added")
	}
	if value != 10 {
		t.Error("The correct stake value was not added")
	}
}

func TestUpdateStakingWithdrawal(t *testing.T) {
	_, pubKey := utils.GenKey("1", "2")
	leader := p2p.Peer{IP: "127.0.0.1", Port: "8882", PubKey: pubKey}
	validator := p2p.Peer{IP: "127.0.0.1", Port: "8885"}
	priKey, _, _ := utils.GenKeyP2P("127.0.0.1", "9902")
	host, err := p2pimpl.NewHost(&leader, priKey)
	if err != nil {
		t.Fatalf("newhost failure: %v", err)
	}
	consensus := consensus.New(host, "0", []p2p.Peer{leader, validator}, leader)

	node := New(host, consensus, nil)
	node.CurrentStakes = make(map[common.Address]int64)

	DepositContractPriKey, _ := crypto.GenerateKey()                                  //DepositContractPriKey is pk for contract
	DepositContractAddress := crypto.PubkeyToAddress(DepositContractPriKey.PublicKey) //DepositContractAddress is the address for the contract
	node.StakingContractAddress = DepositContractAddress
	node.AccountKey, _ = crypto.GenerateKey()
	Address := crypto.PubkeyToAddress(node.AccountKey.PublicKey)
	node.CurrentStakes[Address] = int64(1010)

	withdrawFnSignature := []byte("withdraw(uint)")
	hash := sha3.NewLegacyKeccak256()
	hash.Write(withdrawFnSignature)
	methodID := hash.Sum(nil)[:4]

	stake := "1000"
	amount := new(big.Int)
	amount.SetString(stake, 10)
	paddedAmount := common.LeftPadBytes(amount.Bytes(), 32)

	var dataEnc []byte
	dataEnc = append(dataEnc, methodID...)
	dataEnc = append(dataEnc, paddedAmount...)
	tx, err := types.SignTx(types.NewTransaction(0, DepositContractAddress, node.Consensus.ShardID, big.NewInt(0), params.TxGasContractCreation*10, nil, dataEnc), types.HomesteadSigner{}, node.AccountKey)

	var txs []*types.Transaction
	txs = append(txs, tx)
	header := &types.Header{Extra: []byte("hello")}
	block := types.NewBlock(header, txs, nil)
	node.UpdateStakingList(block)
	value, ok := node.CurrentStakes[Address]
	if !ok {
		t.Error("The correct address was not present")
	}
	if value != 10 {
		t.Error("The correct stake value was not subtracted")
	}

}
