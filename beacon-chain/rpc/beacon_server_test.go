package rpc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gogo/protobuf/proto"
	ptypes "github.com/gogo/protobuf/types"
	"github.com/golang/mock/gomock"
	"github.com/prysmaticlabs/prysm/beacon-chain/internal"
	pbp2p "github.com/prysmaticlabs/prysm/proto/beacon/p2p/v1"
	pb "github.com/prysmaticlabs/prysm/proto/beacon/rpc/v1"
	"github.com/prysmaticlabs/prysm/shared/bytesutil"
	"github.com/prysmaticlabs/prysm/shared/event"
	"github.com/prysmaticlabs/prysm/shared/hashutil"
	"github.com/prysmaticlabs/prysm/shared/params"
	"github.com/prysmaticlabs/prysm/shared/testutil"
	"github.com/prysmaticlabs/prysm/shared/trieutil"
	logTest "github.com/sirupsen/logrus/hooks/test"
)

var closedContext = "context closed"

type faultyPOWChainService struct {
	chainStartFeed *event.Feed
	hashesByHeight map[int][]byte
}

func (f *faultyPOWChainService) HasChainStartLogOccurred() (bool, error) {
	return false, nil
}
func (f *faultyPOWChainService) ETH2GenesisTime() (uint64, error) {
	return 0, nil
}

func (f *faultyPOWChainService) ChainStartFeed() *event.Feed {
	return f.chainStartFeed
}
func (f *faultyPOWChainService) LatestBlockHeight() *big.Int {
	return big.NewInt(0)
}

func (f *faultyPOWChainService) BlockExists(_ context.Context, hash common.Hash) (bool, *big.Int, error) {
	if f.hashesByHeight == nil {
		return false, big.NewInt(1), errors.New("failed")
	}

	return true, big.NewInt(1), nil
}

func (f *faultyPOWChainService) BlockHashByHeight(_ context.Context, height *big.Int) (common.Hash, error) {
	return [32]byte{}, errors.New("failed")
}

func (f *faultyPOWChainService) BlockTimeByHeight(_ context.Context, height *big.Int) (uint64, error) {
	return 0, errors.New("failed")
}

func (f *faultyPOWChainService) DepositRoot() [32]byte {
	return [32]byte{}
}

func (f *faultyPOWChainService) DepositTrie() *trieutil.MerkleTrie {
	return &trieutil.MerkleTrie{}
}

func (f *faultyPOWChainService) ChainStartDeposits() []*pbp2p.Deposit {
	return []*pbp2p.Deposit{}
}

func (f *faultyPOWChainService) ChainStartDepositHashes() ([][]byte, error) {
	return [][]byte{}, errors.New("hashing failed")
}

type mockPOWChainService struct {
	chainStartFeed    *event.Feed
	latestBlockNumber *big.Int
	hashesByHeight    map[int][]byte
	blockTimeByHeight map[int]uint64
}

func (m *mockPOWChainService) HasChainStartLogOccurred() (bool, error) {
	return true, nil
}

func (m *mockPOWChainService) ETH2GenesisTime() (uint64, error) {
	return uint64(time.Unix(0, 0).Unix()), nil
}
func (m *mockPOWChainService) ChainStartFeed() *event.Feed {
	return m.chainStartFeed
}
func (m *mockPOWChainService) LatestBlockHeight() *big.Int {
	return m.latestBlockNumber
}

func (m *mockPOWChainService) DepositTrie() *trieutil.MerkleTrie {
	return &trieutil.MerkleTrie{}
}

func (m *mockPOWChainService) BlockExists(_ context.Context, hash common.Hash) (bool, *big.Int, error) {
	// Reverse the map of heights by hash.
	heightsByHash := make(map[[32]byte]int)
	for k, v := range m.hashesByHeight {
		h := bytesutil.ToBytes32(v)
		heightsByHash[h] = k
	}
	val, ok := heightsByHash[hash]
	if !ok {
		return false, nil, fmt.Errorf("could not fetch height for hash: %#x", hash)
	}
	return true, big.NewInt(int64(val)), nil
}

func (m *mockPOWChainService) BlockHashByHeight(_ context.Context, height *big.Int) (common.Hash, error) {
	k := int(height.Int64())
	val, ok := m.hashesByHeight[k]
	if !ok {
		return [32]byte{}, fmt.Errorf("could not fetch hash for height: %v", height)
	}
	return bytesutil.ToBytes32(val), nil
}

func (m *mockPOWChainService) BlockTimeByHeight(_ context.Context, height *big.Int) (uint64, error) {
	h := int(height.Int64())
	return m.blockTimeByHeight[h], nil
}

func (m *mockPOWChainService) DepositRoot() [32]byte {
	root := []byte("depositroot")
	return bytesutil.ToBytes32(root)
}

func (m *mockPOWChainService) ChainStartDeposits() []*pbp2p.Deposit {
	return []*pbp2p.Deposit{}
}

func (m *mockPOWChainService) ChainStartDepositHashes() ([][]byte, error) {
	return [][]byte{}, nil
}

func TestWaitForChainStart_ContextClosed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	beaconServer := &BeaconServer{
		ctx: ctx,
		powChainService: &faultyPOWChainService{
			chainStartFeed: new(event.Feed),
		},
		chainService: newMockChainService(),
	}
	exitRoutine := make(chan bool)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockStream := internal.NewMockBeaconService_WaitForChainStartServer(ctrl)
	go func(tt *testing.T) {
		if err := beaconServer.WaitForChainStart(&ptypes.Empty{}, mockStream); !strings.Contains(err.Error(), closedContext) {
			tt.Errorf("Could not call RPC method: %v", err)
		}
		<-exitRoutine
	}(t)
	cancel()
	exitRoutine <- true
}

func TestWaitForChainStart_AlreadyStarted(t *testing.T) {
	beaconServer := &BeaconServer{
		ctx: context.Background(),
		powChainService: &mockPOWChainService{
			chainStartFeed: new(event.Feed),
		},
		chainService: newMockChainService(),
	}
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockStream := internal.NewMockBeaconService_WaitForChainStartServer(ctrl)
	mockStream.EXPECT().Send(
		&pb.ChainStartResponse{
			Started:     true,
			GenesisTime: uint64(time.Unix(0, 0).Unix()),
		},
	).Return(nil)
	if err := beaconServer.WaitForChainStart(&ptypes.Empty{}, mockStream); err != nil {
		t.Errorf("Could not call RPC method: %v", err)
	}
}

func TestWaitForChainStart_NotStartedThenLogFired(t *testing.T) {
	hook := logTest.NewGlobal()
	beaconServer := &BeaconServer{
		ctx:            context.Background(),
		chainStartChan: make(chan time.Time, 1),
		powChainService: &faultyPOWChainService{
			chainStartFeed: new(event.Feed),
		},
		chainService: newMockChainService(),
	}
	exitRoutine := make(chan bool)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockStream := internal.NewMockBeaconService_WaitForChainStartServer(ctrl)
	mockStream.EXPECT().Send(
		&pb.ChainStartResponse{
			Started:     true,
			GenesisTime: uint64(time.Unix(0, 0).Unix()),
		},
	).Return(nil)
	go func(tt *testing.T) {
		if err := beaconServer.WaitForChainStart(&ptypes.Empty{}, mockStream); err != nil {
			tt.Errorf("Could not call RPC method: %v", err)
		}
		<-exitRoutine
	}(t)
	beaconServer.chainStartChan <- time.Unix(0, 0)
	exitRoutine <- true
	testutil.AssertLogsContain(t, hook, "Sending ChainStart log and genesis time to connected validator clients")
}

func TestLatestAttestation_ContextClosed(t *testing.T) {
	hook := logTest.NewGlobal()
	mockOperationService := &mockOperationService{}
	ctx, cancel := context.WithCancel(context.Background())
	beaconServer := &BeaconServer{
		ctx:              ctx,
		operationService: mockOperationService,
		chainService:     newMockChainService(),
	}
	exitRoutine := make(chan bool)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockStream := internal.NewMockBeaconService_LatestAttestationServer(ctrl)
	go func(tt *testing.T) {
		if err := beaconServer.LatestAttestation(&ptypes.Empty{}, mockStream); err != nil {
			tt.Errorf("Could not call RPC method: %v", err)
		}
		<-exitRoutine
	}(t)
	cancel()
	exitRoutine <- true
	testutil.AssertLogsContain(t, hook, "RPC context closed, exiting goroutine")
}

func TestLatestAttestation_FaultyServer(t *testing.T) {
	mockOperationService := &mockOperationService{}
	ctx, cancel := context.WithCancel(context.Background())
	beaconServer := &BeaconServer{
		ctx:                 ctx,
		operationService:    mockOperationService,
		incomingAttestation: make(chan *pbp2p.Attestation, 0),
		chainService:        newMockChainService(),
	}
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	exitRoutine := make(chan bool)
	attestation := &pbp2p.Attestation{}

	mockStream := internal.NewMockBeaconService_LatestAttestationServer(ctrl)
	mockStream.EXPECT().Send(attestation).Return(errors.New("something wrong"))
	// Tests a faulty stream.
	go func(tt *testing.T) {
		if err := beaconServer.LatestAttestation(&ptypes.Empty{}, mockStream); err.Error() != "something wrong" {
			tt.Errorf("Faulty stream should throw correct error, wanted 'something wrong', got %v", err)
		}
		<-exitRoutine
	}(t)

	beaconServer.incomingAttestation <- attestation
	cancel()
	exitRoutine <- true
}

func TestLatestAttestation_SendsCorrectly(t *testing.T) {
	hook := logTest.NewGlobal()
	operationService := &mockOperationService{}
	ctx, cancel := context.WithCancel(context.Background())
	beaconServer := &BeaconServer{
		ctx:                 ctx,
		operationService:    operationService,
		incomingAttestation: make(chan *pbp2p.Attestation, 0),
		chainService:        newMockChainService(),
	}
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	exitRoutine := make(chan bool)
	attestation := &pbp2p.Attestation{}
	mockStream := internal.NewMockBeaconService_LatestAttestationServer(ctrl)
	mockStream.EXPECT().Send(attestation).Return(nil)
	// Tests a good stream.
	go func(tt *testing.T) {
		if err := beaconServer.LatestAttestation(&ptypes.Empty{}, mockStream); err != nil {
			tt.Errorf("Could not call RPC method: %v", err)
		}
		<-exitRoutine
	}(t)
	beaconServer.incomingAttestation <- attestation
	cancel()
	exitRoutine <- true

	testutil.AssertLogsContain(t, hook, "Sending attestation to RPC clients")
}

func TestPendingDeposits_UnknownBlockNum(t *testing.T) {
	p := &mockPOWChainService{
		latestBlockNumber: nil,
	}
	bs := BeaconServer{powChainService: p}

	_, err := bs.PendingDeposits(context.Background(), nil)
	if err.Error() != "latest PoW block number is unknown" {
		t.Errorf("Received unexpected error: %v", err)
	}
}

func TestPendingDeposits_OutsideEth1FollowWindow(t *testing.T) {
	ctx := context.Background()

	height := big.NewInt(int64(params.BeaconConfig().Eth1FollowDistance))
	p := &mockPOWChainService{
		latestBlockNumber: height,
		hashesByHeight: map[int][]byte{
			int(height.Int64()): []byte("0x0"),
		},
	}
	d := internal.SetupDB(t)

	beaconState := &pbp2p.BeaconState{
		LatestEth1Data: &pbp2p.Eth1Data{
			BlockRoot: []byte("0x0"),
		},
		DepositIndex: 2,
	}
	if err := d.SaveState(ctx, beaconState); err != nil {
		t.Fatal(err)
	}

	// Using the merkleTreeIndex as the block number for this test...
	readyDeposits := []*pbp2p.Deposit{
		{
			Index:       0,
			DepositData: []byte("a"),
		},
		{
			Index:       1,
			DepositData: []byte("b"),
		},
	}

	recentDeposits := []*pbp2p.Deposit{
		{
			Index:       2,
			DepositData: []byte("c"),
		},
		{
			Index:       3,
			DepositData: []byte("d"),
		},
	}
	for _, dp := range append(readyDeposits, recentDeposits...) {
		d.InsertDeposit(ctx, dp, big.NewInt(int64(dp.Index)))
	}
	for _, dp := range recentDeposits {
		d.InsertPendingDeposit(ctx, dp, big.NewInt(int64(dp.Index)))
	}

	bs := &BeaconServer{
		beaconDB:        d,
		powChainService: p,
		chainService:    newMockChainService(),
	}

	result, err := bs.PendingDeposits(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.PendingDeposits) != 0 {
		t.Errorf("Received unexpected list of deposits: %+v, wanted: 0", len(result.PendingDeposits))
	}

	// It should also return the recent deposits after their follow window.
	p.latestBlockNumber = big.NewInt(0).Add(p.latestBlockNumber, big.NewInt(10000))
	allResp, err := bs.PendingDeposits(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(allResp.PendingDeposits) != len(recentDeposits) {
		t.Errorf(
			"Received unexpected number of pending deposits: %d, wanted: %d",
			len(allResp.PendingDeposits),
			len(recentDeposits),
		)
	}
}

func TestPendingDeposits_CantReturnBelowStateDepositIndex(t *testing.T) {
	ctx := context.Background()

	height := big.NewInt(int64(params.BeaconConfig().Eth1FollowDistance))
	p := &mockPOWChainService{
		latestBlockNumber: height,
		hashesByHeight: map[int][]byte{
			int(height.Int64()): []byte("0x0"),
		},
	}
	d := internal.SetupDB(t)

	beaconState := &pbp2p.BeaconState{
		LatestEth1Data: &pbp2p.Eth1Data{
			BlockRoot: []byte("0x0"),
		},
		DepositIndex: 10,
	}
	if err := d.SaveState(ctx, beaconState); err != nil {
		t.Fatal(err)
	}

	readyDeposits := []*pbp2p.Deposit{
		{
			Index:       0,
			DepositData: []byte("a"),
		},
		{
			Index:       1,
			DepositData: []byte("b"),
		},
	}

	var recentDeposits []*pbp2p.Deposit
	for i := 2; i < 16; i++ {
		recentDeposits = append(recentDeposits, &pbp2p.Deposit{
			Index:       uint64(i),
			DepositData: []byte{byte(i)},
		})
	}

	for _, dp := range append(readyDeposits, recentDeposits...) {
		d.InsertDeposit(ctx, dp, big.NewInt(int64(dp.Index)))
	}
	for _, dp := range recentDeposits {
		d.InsertPendingDeposit(ctx, dp, big.NewInt(int64(dp.Index)))
	}

	bs := &BeaconServer{
		beaconDB:        d,
		powChainService: p,
		chainService:    newMockChainService(),
	}

	// It should also return the recent deposits after their follow window.
	p.latestBlockNumber = big.NewInt(0).Add(p.latestBlockNumber, big.NewInt(10000))
	allResp, err := bs.PendingDeposits(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	expectedDeposits := 6
	if len(allResp.PendingDeposits) != expectedDeposits {
		t.Errorf(
			"Received unexpected number of pending deposits: %d, wanted: %d",
			len(allResp.PendingDeposits),
			expectedDeposits,
		)
	}
	if allResp.PendingDeposits[0].Index != beaconState.DepositIndex {
		t.Errorf(
			"Received unexpected merkle index: %d, wanted: %d",
			allResp.PendingDeposits[0].Index,
			beaconState.DepositIndex,
		)
	}
}

func TestPendingDeposits_CantReturnMoreThanMax(t *testing.T) {
	ctx := context.Background()

	height := big.NewInt(int64(params.BeaconConfig().Eth1FollowDistance))
	p := &mockPOWChainService{
		latestBlockNumber: height,
		hashesByHeight: map[int][]byte{
			int(height.Int64()): []byte("0x0"),
		},
	}
	d := internal.SetupDB(t)

	beaconState := &pbp2p.BeaconState{
		LatestEth1Data: &pbp2p.Eth1Data{
			BlockRoot: []byte("0x0"),
		},
		DepositIndex: 2,
	}
	if err := d.SaveState(ctx, beaconState); err != nil {
		t.Fatal(err)
	}

	readyDeposits := []*pbp2p.Deposit{
		{
			Index:       0,
			DepositData: []byte("a"),
		},
		{
			Index:       1,
			DepositData: []byte("b"),
		},
	}

	var recentDeposits []*pbp2p.Deposit
	for i := 2; i < 22; i++ {
		recentDeposits = append(recentDeposits, &pbp2p.Deposit{
			Index:       uint64(i),
			DepositData: []byte{byte(i)},
		})
	}

	for _, dp := range append(readyDeposits, recentDeposits...) {
		d.InsertDeposit(ctx, dp, big.NewInt(int64(dp.Index)))
	}
	for _, dp := range recentDeposits {
		d.InsertPendingDeposit(ctx, dp, big.NewInt(int64(dp.Index)))
	}

	bs := &BeaconServer{
		beaconDB:        d,
		powChainService: p,
		chainService:    newMockChainService(),
	}

	// It should also return the recent deposits after their follow window.
	p.latestBlockNumber = big.NewInt(0).Add(p.latestBlockNumber, big.NewInt(10000))
	allResp, err := bs.PendingDeposits(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(allResp.PendingDeposits) != int(params.BeaconConfig().MaxDeposits) {
		t.Errorf(
			"Received unexpected number of pending deposits: %d, wanted: %d",
			len(allResp.PendingDeposits),
			int(params.BeaconConfig().MaxDeposits),
		)
	}
}

func TestEth1Data_EmptyVotesFetchBlockHashFailure(t *testing.T) {
	t.Skip()
	db := internal.SetupDB(t)
	defer internal.TeardownDB(t, db)
	ctx := context.Background()

	beaconServer := &BeaconServer{
		beaconDB: db,
		powChainService: &faultyPOWChainService{
			hashesByHeight: make(map[int][]byte),
		},
	}
	beaconState := &pbp2p.BeaconState{
		LatestEth1Data: &pbp2p.Eth1Data{
			BlockRoot: []byte{'a'},
		},
		Eth1DataVotes: []*pbp2p.Eth1Data{},
	}
	if err := beaconServer.beaconDB.SaveState(ctx, beaconState); err != nil {
		t.Fatal(err)
	}
	want := "could not fetch ETH1_FOLLOW_DISTANCE ancestor"
	if _, err := beaconServer.Eth1Data(context.Background(), nil); !strings.Contains(err.Error(), want) {
		t.Errorf("Expected error %v, received %v", want, err)
	}
}

func TestEth1Data_EmptyVotesOk(t *testing.T) {
	t.Skip()
	db := internal.SetupDB(t)
	defer internal.TeardownDB(t, db)
	ctx := context.Background()

	height := big.NewInt(int64(params.BeaconConfig().Eth1FollowDistance))
	deps := []*pbp2p.Deposit{
		{Index: 0, DepositData: []byte("a")},
		{Index: 1, DepositData: []byte("b")},
	}
	depsData := [][]byte{}
	for _, dp := range deps {
		db.InsertDeposit(context.Background(), dp, big.NewInt(0))
		depsData = append(depsData, dp.DepositData)
	}

	depositTrie, err := trieutil.GenerateTrieFromItems(depsData, int(params.BeaconConfig().DepositContractTreeDepth))
	if err != nil {
		t.Fatal(err)
	}
	depositRoot := depositTrie.Root()
	beaconState := &pbp2p.BeaconState{
		LatestEth1Data: &pbp2p.Eth1Data{
			BlockRoot:   []byte("hash0"),
			DepositRoot: depositRoot[:],
		},
		Eth1DataVotes: []*pbp2p.Eth1Data{},
	}

	powChainService := &mockPOWChainService{
		latestBlockNumber: height,
		hashesByHeight: map[int][]byte{
			0: []byte("hash0"),
			1: beaconState.LatestEth1Data.BlockRoot,
		},
	}
	beaconServer := &BeaconServer{
		beaconDB:        db,
		powChainService: powChainService,
	}

	if err := beaconServer.beaconDB.SaveState(ctx, beaconState); err != nil {
		t.Fatal(err)
	}
	result, err := beaconServer.Eth1Data(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// If the data vote objects are empty, the deposit root should be the one corresponding
	// to the deposit contract in the powchain service, fetched using powChainService.DepositRoot()
	if !bytes.Equal(result.Eth1Data.DepositRoot, depositRoot[:]) {
		t.Errorf(
			"Expected deposit roots to match, received %#x == %#x",
			result.Eth1Data.DepositRoot,
			depositRoot,
		)
	}
}

func TestEth1Data_NonEmptyVotesSelectsBestVote(t *testing.T) {
	t.Skip()
	db := internal.SetupDB(t)
	defer internal.TeardownDB(t, db)
	ctx := context.Background()

	eth1DataVotes := []*pbp2p.Eth1Data{}
	beaconState := &pbp2p.BeaconState{
		Eth1DataVotes: eth1DataVotes,
		LatestEth1Data: &pbp2p.Eth1Data{
			BlockRoot: []byte("stub"),
		},
	}
	if err := db.SaveState(ctx, beaconState); err != nil {
		t.Fatal(err)
	}
	currentHeight := params.BeaconConfig().Eth1FollowDistance + 5
	beaconServer := &BeaconServer{
		beaconDB: db,
		powChainService: &mockPOWChainService{
			latestBlockNumber: big.NewInt(int64(currentHeight)),
			hashesByHeight: map[int][]byte{
				0: beaconState.LatestEth1Data.BlockRoot,
				1: beaconState.Eth1DataVotes[0].BlockRoot,
				2: beaconState.Eth1DataVotes[1].BlockRoot,
				3: beaconState.Eth1DataVotes[3].BlockRoot,
				// We will give the hash at index 2 in the beacon state's latest eth1 votes
				// priority in being selected as the best vote by giving it the highest block number.
				4: beaconState.Eth1DataVotes[2].BlockRoot,
			},
		},
	}
	result, err := beaconServer.Eth1Data(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Vote at index 2 should have won the best vote selection mechanism as it had the highest block number
	// despite being tied at vote count with the vote at index 3.
	if !bytes.Equal(result.Eth1Data.BlockRoot, beaconState.Eth1DataVotes[2].BlockRoot) {
		t.Errorf(
			"Expected block hashes to match, received %#x == %#x",
			result.Eth1Data.BlockRoot,
			beaconState.Eth1DataVotes[2].BlockRoot,
		)
	}
	if !bytes.Equal(result.Eth1Data.DepositRoot, beaconState.Eth1DataVotes[2].DepositRoot) {
		t.Errorf(
			"Expected deposit roots to match, received %#x == %#x",
			result.Eth1Data.DepositRoot,
			beaconState.Eth1DataVotes[2].DepositRoot,
		)
	}
}

func TestBlockTree_OK(t *testing.T) {
	db := internal.SetupDB(t)
	defer internal.TeardownDB(t, db)
	// We want to ensure that if our block tree looks as follows, the RPC response
	// returns the correct information.
	//                   /->[A, Slot 3, 3 Votes]->[B, Slot 4, 3 Votes]
	// [Justified Block]->[C, Slot 3, 2 Votes]
	//                   \->[D, Slot 3, 2 Votes]->[SKIP SLOT]->[E, Slot 5, 1 Vote]
	var validators []*pbp2p.Validator
	for i := 0; i < 11; i++ {
		validators = append(validators, &pbp2p.Validator{EffectiveBalance: params.BeaconConfig().MaxEffectiveBalance})
	}
	justifiedState := &pbp2p.BeaconState{
		Slot:              0,
		ValidatorRegistry: validators,
	}

	if err := db.SaveJustifiedState(justifiedState); err != nil {
		t.Fatal(err)
	}
	justifiedBlock := &pbp2p.BeaconBlock{
		Slot: 0,
	}
	if err := db.SaveJustifiedBlock(justifiedBlock); err != nil {
		t.Fatal(err)
	}
	justifiedRoot, _ := hashutil.HashBeaconBlock(justifiedBlock)
	b1 := &pbp2p.BeaconBlock{
		Slot:            3,
		ParentBlockRoot: justifiedRoot[:],
		RandaoReveal:    []byte("A"),
	}
	b1Root, _ := hashutil.HashBeaconBlock(b1)
	b2 := &pbp2p.BeaconBlock{
		Slot:            3,
		ParentBlockRoot: justifiedRoot[:],
		RandaoReveal:    []byte("C"),
	}
	b2Root, _ := hashutil.HashBeaconBlock(b2)
	b3 := &pbp2p.BeaconBlock{
		Slot:            3,
		ParentBlockRoot: justifiedRoot[:],
		RandaoReveal:    []byte("D"),
	}
	b3Root, _ := hashutil.HashBeaconBlock(b1)
	b4 := &pbp2p.BeaconBlock{
		Slot:            4,
		ParentBlockRoot: b1Root[:],
		RandaoReveal:    []byte("B"),
	}
	b4Root, _ := hashutil.HashBeaconBlock(b4)
	b5 := &pbp2p.BeaconBlock{
		Slot:            5,
		ParentBlockRoot: b3Root[:],
		RandaoReveal:    []byte("E"),
	}
	b5Root, _ := hashutil.HashBeaconBlock(b5)

	attestationTargets := make(map[uint64]*pbp2p.AttestationTarget)
	// We give block A 3 votes.
	attestationTargets[0] = &pbp2p.AttestationTarget{
		Slot:       b1.Slot,
		ParentRoot: b1.ParentBlockRoot,
		BlockRoot:  b1Root[:],
	}
	attestationTargets[1] = &pbp2p.AttestationTarget{
		Slot:       b1.Slot,
		ParentRoot: b1.ParentBlockRoot,
		BlockRoot:  b1Root[:],
	}
	attestationTargets[2] = &pbp2p.AttestationTarget{
		Slot:       b1.Slot,
		ParentRoot: b1.ParentBlockRoot,
		BlockRoot:  b1Root[:],
	}

	// We give block C 2 votes.
	attestationTargets[3] = &pbp2p.AttestationTarget{
		Slot:       b2.Slot,
		ParentRoot: b2.ParentBlockRoot,
		BlockRoot:  b2Root[:],
	}
	attestationTargets[4] = &pbp2p.AttestationTarget{
		Slot:       b2.Slot,
		ParentRoot: b2.ParentBlockRoot,
		BlockRoot:  b2Root[:],
	}

	// We give block D 2 votes.
	attestationTargets[5] = &pbp2p.AttestationTarget{
		Slot:       b3.Slot,
		ParentRoot: b3.ParentBlockRoot,
		BlockRoot:  b3Root[:],
	}
	attestationTargets[6] = &pbp2p.AttestationTarget{
		Slot:       b3.Slot,
		ParentRoot: b3.ParentBlockRoot,
		BlockRoot:  b3Root[:],
	}

	// We give block B 3 votes.
	attestationTargets[7] = &pbp2p.AttestationTarget{
		Slot:       b4.Slot,
		ParentRoot: b4.ParentBlockRoot,
		BlockRoot:  b4Root[:],
	}
	attestationTargets[8] = &pbp2p.AttestationTarget{
		Slot:       b4.Slot,
		ParentRoot: b4.ParentBlockRoot,
		BlockRoot:  b4Root[:],
	}
	attestationTargets[9] = &pbp2p.AttestationTarget{
		Slot:       b4.Slot,
		ParentRoot: b4.ParentBlockRoot,
		BlockRoot:  b4Root[:],
	}

	// We give block E 1 vote.
	attestationTargets[10] = &pbp2p.AttestationTarget{
		Slot:       b5.Slot,
		ParentRoot: b5.ParentBlockRoot,
		BlockRoot:  b5Root[:],
	}

	tree := []*pb.BlockTreeResponse_TreeNode{
		{
			Block: b1,
			Votes: 3 * params.BeaconConfig().MaxDepositAmount,
		},
		{
			Block: b2,
			Votes: 2 * params.BeaconConfig().MaxDepositAmount,
		},
		{
			Block: b3,
			Votes: 2 * params.BeaconConfig().MaxDepositAmount,
		},
		{
			Block: b4,
			Votes: 3 * params.BeaconConfig().MaxDepositAmount,
		},
		{
			Block: b5,
			Votes: 1 * params.BeaconConfig().MaxDepositAmount,
		},
	}
	for _, node := range tree {
		if err := db.SaveBlock(node.Block); err != nil {
			t.Fatal(err)
		}
	}

	headState := &pbp2p.BeaconState{
		Slot: b4.Slot,
	}
	ctx := context.Background()
	if err := db.UpdateChainHead(ctx, b4, headState); err != nil {
		t.Fatal(err)
	}

	bs := &BeaconServer{
		beaconDB:       db,
		targetsFetcher: &mockChainService{targets: attestationTargets},
	}
	resp, err := bs.BlockTree(ctx, &ptypes.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(resp.Tree, func(i, j int) bool {
		return string(resp.Tree[i].Block.RandaoReveal) < string(resp.Tree[j].Block.RandaoReveal)
	})
	sort.Slice(tree, func(i, j int) bool {
		return string(tree[i].Block.RandaoReveal) < string(tree[j].Block.RandaoReveal)
	})
	for i := range resp.Tree {
		if !proto.Equal(resp.Tree[i].Block, tree[i].Block) {
			t.Errorf("Expected %v, received %v", tree[i].Block, resp.Tree[i].Block)
		}
	}
}

func Benchmark_Eth1Data(b *testing.B) {
	db := internal.SetupDB(b)
	defer internal.TeardownDB(b, db)
	ctx := context.Background()

	hashesByHeight := make(map[int][]byte)

	beaconState := &pbp2p.BeaconState{
		Eth1DataVotes: []*pbp2p.Eth1Data{},
		LatestEth1Data: &pbp2p.Eth1Data{
			BlockRoot: []byte("stub"),
		},
	}
	numOfVotes := 1000
	for i := 0; i < numOfVotes; i++ {
		blockhash := []byte{'b', 'l', 'o', 'c', 'k', byte(i)}
		deposit := []byte{'d', 'e', 'p', 'o', 's', 'i', 't', byte(i)}
		beaconState.Eth1DataVotes = append(beaconState.Eth1DataVotes, &pbp2p.Eth1Data{
			BlockRoot:   blockhash,
			DepositRoot: deposit,
		})
		hashesByHeight[i] = blockhash
	}
	hashesByHeight[numOfVotes+1] = []byte("stub")

	if err := db.SaveState(ctx, beaconState); err != nil {
		b.Fatal(err)
	}
	currentHeight := params.BeaconConfig().Eth1FollowDistance + 5
	beaconServer := &BeaconServer{
		beaconDB: db,
		powChainService: &mockPOWChainService{
			latestBlockNumber: big.NewInt(int64(currentHeight)),
			hashesByHeight:    hashesByHeight,
		},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := beaconServer.Eth1Data(context.Background(), nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}
