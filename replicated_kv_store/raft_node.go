package main

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/krithikvaidya/distributed-dns/replicated_kv_store/protos"
	"google.golang.org/grpc"
)

type RaftNodeState int

const (
	Follower RaftNodeState = iota
	Candidate
	Leader
	Down
)

/*
 * Struct for representing a single log entry. An array of this is stored
 * on the raft node
 */
type LogEntry struct {
	term int
	value int
}

// Main struct storing different aspects of the replica and it's state
// Refer to figure 2 in the paper
type RaftNode struct {
	protos.UnimplementedConsensusServiceServer
	ready_chan           chan bool
	n_replicas           int
	replicas_ready       int // number of replicas that have connected to this replica's gRPC server.
	replica_id           int
	peer_replica_clients []protos.ConsensusServiceClient // client objects to send messages to other peers
	raft_node_mutex      sync.Mutex
	node_state           RaftNodeState

	// States mentioned in figure 2 of the paper:

	// State to be maintained on all replicas (TODO: persist)
	currentTerm int
	votedFor    int
	log         []LogEntry

	// State to be maintained on all replicas
	commitIndex        int
	lastApplied        int
	state              RaftNodeState
	electionResetEvent time.Time

	// State to be maintained on the leader
	nextIndex  []int
	matchIndex []int
}

func InitializeNode(n_replica int, rid int) *RaftNode {

	rn := &RaftNode{

		n_replicas:           n_replica,
		ready_chan:           make(chan bool),
		replicas_ready:       0,
		replica_id:           rid,
		peer_replica_clients: make([]protos.ConsensusServiceClient, n_replica),
		node_state:           Follower, // all nodes are initialized as followers

		currentTerm: 0, // unpersisted
		votedFor:    -1,
		log:         make([]LogEntry, 10000), // initialized with fixed capacity of 10000, change later.

		commitIndex: 0, // index of highest log entry known to be committed.
		lastApplied: 0, // index of highest log entry applied to state machine.
	}

	return rn

}

func (node *RaftNode) ConnectToPeerReplicas(rep_addrs []string) {

	// Attempt to connect to the gRPC servers of all other replicas
	// The clients for each corresponding server is stored in client_objs
	client_objs := make([]protos.ConsensusServiceClient, node.n_replicas)

	for i := 0; i < node.n_replicas; i++ {

		if i == node.replica_id {
			continue
		}

		connxn, err := grpc.Dial(rep_addrs[i], grpc.WithInsecure())
		CheckError(err)

		// Obtain client stub
		cli := protos.NewConsensusServiceClient(connxn)

		client_objs[i] = cli

		// ReplicaReady is an RPC defined to inform the other replica about our connection
		_, err = cli.ReplicaReady(context.Background(), &empty.Empty{})
		CheckError(err)

	}

	node.peer_replica_clients = client_objs

}

// RPC declared in protos/replica.proto.
// When a replica performs the gRPC dial to another replica and obtains the
// corresponding client stub, it will invoke this RPC to inform the other replica
// that it has connected.
func (node *RaftNode) ReplicaReady(ctx context.Context, in *empty.Empty) (*empty.Empty, error) {

	node.raft_node_mutex.Lock() // Multiple instances of ReplicaReady method may run parallely

	log.Printf("\nReceived ReplicaReady Notification\n")
	node.replicas_ready += 1

	if node.replicas_ready == node.n_replicas-1 {

		// Using defer does not work here. Not sure why
		go func(node *RaftNode) { node.ready_chan <- true }(node)

		log.Printf("\nAll replicas have connected.\n")

	}

	node.raft_node_mutex.Unlock()

	return &empty.Empty{}, nil
}
