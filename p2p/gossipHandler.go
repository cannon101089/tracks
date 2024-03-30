package p2p

import (
	"encoding/json"
	"fmt"
	mock "github.com/airchains-network/decentralized-sequencer/da/mockda"
	"github.com/airchains-network/decentralized-sequencer/junction"
	junctionTypes "github.com/airchains-network/decentralized-sequencer/junction/types"
	logs "github.com/airchains-network/decentralized-sequencer/log"
	"github.com/airchains-network/decentralized-sequencer/node/shared"
	"github.com/airchains-network/decentralized-sequencer/types"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"math/rand"
	"time"
)

func ProcessGossipMessage(node host.Host, dataType string, dataByte []byte, messageBroadcaster peer.ID) {
	_ = node
	switch dataType {
	case "vrfInitiated":
		VRFInitiatedMsgHandler(dataByte)
		break
	case "vrnValidated":
		VRNValidatedMsgHandler(dataByte)
		break
	case "podSubmitted":
		PodSubmittedMsgHandler(dataByte)
		break
	case "podVerified":
		PodVerifiedMsgHandler(dataByte)
		break
	default:
		logs.Log.Error("Unknown gossip data type founded")
		break
	}

	return
}

func VRFInitiatedMsgHandler(dataByte []byte) {
	// decode and get who is the verifier of vrf
	var VRFInitiatedMsg VRFInitiatedMsg
	if err := json.Unmarshal(dataByte, &VRFInitiatedMsg); err != nil {
		logs.Log.Info("Error in extracting VRFInitiatedMsg")
		return
	}

	// match the pod number
	for {
		// get latest updated pod state
		podState := shared.GetPodState()

		// wait until pod number reached
		if VRFInitiatedMsg.PodNumber == podState.LatestPodHeight {
			// exit from the loop
			logs.Log.Info("Pod number matched")
			break
		}

		logs.Log.Warn("pod number is not matched, waiting for new pod")
		fmt.Println(VRFInitiatedMsg.PodNumber, podState.LatestPodHeight)
		// Await for 3 seconds
		time.Sleep(3 * time.Second)
	}

	_, _, accountPath, accountName, addressPrefix, tracks, err := junction.GetJunctionDetails()
	if err != nil {
		logs.Log.Error("can not get junctionDetails.json data: " + err.Error())
		return
	}
	myAddress, err := junction.CheckIfAccountExists(accountName, accountPath, addressPrefix)
	if err != nil {
		logs.Log.Error("Can not get junction wallet address")
		return
	}

	logs.Log.Info("selected address: " + VRFInitiatedMsg.SelectedTrackAddress)
	logs.Log.Info("My address:" + myAddress)
	// now check for this pod number, who is the selected node
	if VRFInitiatedMsg.SelectedTrackAddress == myAddress {
		logs.Log.Info("This Track Address is selected to verify VRN")
		// call Verify VRN
		VrfInitiatorAddress := VRFInitiatedMsg.VrfInitiatorAddress

		// upper bond
		success := junction.ValidateVRF(VrfInitiatorAddress)
		if !success {
			logs.Log.Error("Failed to Validate VRF")
			return
		}
		logs.Log.Info("validate vrf Transaction success")

		// check if VRN is successfully validated
		var vrfRecord *junctionTypes.VrfRecord
		vrfRecord = junction.QueryVRF()
		if vrfRecord == nil {
			logs.Log.Error("VRF record is nil")
			return
		}
		if !vrfRecord.IsVerified {
			logs.Log.Error("Verification of VRF is failed, need Voting for correct VRN")
			return
		}

		// broadcast message that vrn is verified and selected node is ...
		PodNumber := int(shared.GetPodState().LatestPodHeight)
		SelectedTrackAddress := tracks[vrfRecord.SelectedTrackIndex]
		VRFVerifiedMsg := VRFVerifiedMsg{
			PodNumber:            uint64(PodNumber),
			SelectedTrackAddress: SelectedTrackAddress,
		}
		VRFVerifiedMsgByte, err := json.Marshal(VRFVerifiedMsg)
		if err != nil {
			logs.Log.Error("Error in Marshaling ProofVote Result")
			return
		}
		gossipMsg := types.GossipData{
			Type: "vrnValidated",
			Data: VRFVerifiedMsgByte,
		}
		gossipMsgByte, err := json.Marshal(gossipMsg)
		if err != nil {
			logs.Log.Error("Error marshaling gossip message")
			return
		}
		BroadcastMessage(CTX, Node, gossipMsgByte)

		// if this node is selected as pod submitter
		if SelectedTrackAddress == myAddress {
			VRNValidatedMsgHandler(VRFVerifiedMsgByte)
		}
	}
	return
}

func VRNValidatedMsgHandler(dataByte []byte) {
	fmt.Println("VRN Validated Msg Handler called")
	var VRNVerifiedMsg VRFVerifiedMsg
	if err := json.Unmarshal(dataByte, &VRNVerifiedMsg); err != nil {
		logs.Log.Error("Error in extracting VRFVerifiedMsg")
		return
	}

	// match the pod number
	for {
		podState := shared.GetPodState()
		if VRNVerifiedMsg.PodNumber == podState.LatestPodHeight {
			break
		}
		logs.Log.Warn("pod number is not matched, waiting for new pod")
		fmt.Println(VRNVerifiedMsg.PodNumber, podState.LatestPodHeight)
		time.Sleep(3 * time.Second)
	}

	// check if this node is selected to submit pod & da
	_, _, accountPath, accountName, addressPrefix, tracks, err := junction.GetJunctionDetails()
	if err != nil {
		logs.Log.Error("can not get junctionDetails.json data: " + err.Error())
		return
	}
	myAddress, err := junction.CheckIfAccountExists(accountName, accountPath, addressPrefix)
	if err != nil {
		logs.Log.Error("Can not get junction wallet address")
		return
	}
	// now check for this pod number, who is the selected track
	if VRNVerifiedMsg.SelectedTrackAddress == myAddress {
		// submit data to DA
		DaData := shared.GetPodState().Batch.TransactionHash
		daDataByte := []byte{}
		for _, str := range DaData {
			daDataByte = append(daDataByte, []byte(str)...)
		}
		PodNumber := int(shared.GetPodState().LatestPodHeight)
		connection := shared.Node.NodeConnections
		mdb := connection.GetDataAvailabilityDatabaseConnection()
		dbName, err := mock.MockDA(mdb, daDataByte, PodNumber) // (mockda-%d", batchNumber), nil
		if err != nil {
			logs.Log.Error("Error in submitting data to DA")
			return
		}
		_ = dbName
		logs.Log.Info("data in DA submitted")

		// submit pod to junction
		success := junction.SubmitCurrentPod()
		if !success {
			logs.Log.Error("Failed to submit pod")
			return
		}
		logs.Log.Info("pod submitted")

		var filteredTracks []string
		for _, track := range tracks {
			if track != myAddress {
				filteredTracks = append(filteredTracks, track)
			}
		}
		// Select a random peer from the filtered list
		SelectedTrackAddress := filteredTracks[rand.Intn(len(filteredTracks))]

		podNumber := shared.GetPodState().LatestPodHeight
		// broadcast pod submitted msg
		PodSubmittedMsg := PodSubmittedMsgData{
			PodNumber:            podNumber,
			SelectedTrackAddress: SelectedTrackAddress,
		}
		PodSubmittedMsgByte, err := json.Marshal(PodSubmittedMsg)
		if err != nil {
			logs.Log.Error("Error in Marshaling PodSubmittedMsg")
			return
		}
		gossipMsg := types.GossipData{
			Type: "podSubmitted",
			Data: PodSubmittedMsgByte,
		}
		gossipMsgByte, err := json.Marshal(gossipMsg)
		if err != nil {
			logs.Log.Error("Error marshaling gossip message")
			return
		}
		BroadcastMessage(CTX, Node, gossipMsgByte)
	}
	return
}

func PodSubmittedMsgHandler(dataByte []byte) {

	var PodSubmittedMsg PodSubmittedMsgData
	if err := json.Unmarshal(dataByte, &PodSubmittedMsg); err != nil {
		logs.Log.Error("Error in extracting PodSubmittedMsg")
		return
	}

	// match the pod number
	for {
		podState := shared.GetPodState()
		if PodSubmittedMsg.PodNumber == podState.LatestPodHeight {
			break
		}
		logs.Log.Warn("pod number is not matched, waiting for new pod")
		fmt.Println(PodSubmittedMsg.PodNumber, podState.LatestPodHeight)
		time.Sleep(3 * time.Second)
	}

	// check if this node is selected to verify pod
	_, _, accountPath, accountName, addressPrefix, _, err := junction.GetJunctionDetails()
	if err != nil {
		logs.Log.Error("can not get junctionDetails.json data: " + err.Error())
		return
	}
	myAddress, err := junction.CheckIfAccountExists(accountName, accountPath, addressPrefix)
	if err != nil {
		logs.Log.Error("Can not get junction wallet address")
		return
	}
	// now check for this pod number, who is the selected track
	if PodSubmittedMsg.SelectedTrackAddress == myAddress {
		// verify pod
		success := junction.VerifyCurrentPod()
		if !success {
			logs.Log.Error("Failed to Transact Verify pod")
			return
		}
		logs.Log.Info("pod verification transaction done")

		// Query if pod is really verified
		podDetails := junction.QueryPod(PodSubmittedMsg.PodNumber)
		if podDetails == nil {
			logs.Log.Error("Pod is not submitted properly")
			return
		}
		if podDetails.IsVerified == false {
			logs.Log.Error("Pod verification failed")
			return
		} else {
			// broadcast msg that pod is verified
			podNumber := shared.GetPodState().LatestPodHeight
			PodVerifiedMsg := PodVerifiedMsgData{
				PodNumber:          podNumber,
				VerificationResult: true,
			}
			PodVerifiedMsgByte, err := json.Marshal(PodVerifiedMsg)
			if err != nil {
				logs.Log.Error("Error in Marshaling PodVerifiedMsg")
				return
			}
			gossipMsg := types.GossipData{
				Type: "podVerified",
				Data: PodVerifiedMsgByte,
			}
			gossipMsgByte, err := json.Marshal(gossipMsg)
			if err != nil {
				logs.Log.Error("Error marshaling gossip message")
				return
			}

			saveVerifiedPOD() // save the latest pod details and make next pod
			//
			//// add new nodes requested before starting next pod process
			//peerListLocked = false
			//peerListLock.Unlock()
			//peerListLock.Lock()
			//for _, peerInfo := range incomingPeers.GetPeers() {
			//	peerList.AddPeer(peerInfo)
			//}
			//peerListLock.Unlock() // save data to database

			BroadcastMessage(CTX, Node, gossipMsgByte)
			GenerateUnverifiedPods() // generate next pod
		}
	}
	return
}

func PodVerifiedMsgHandler(dataByte []byte) {
	var PodVerifiedMsg PodVerifiedMsgData
	if err := json.Unmarshal(dataByte, &PodVerifiedMsg); err != nil {
		logs.Log.Error("Error in extracting PodVerifiedMsg")
		return
	}
	logs.Log.Warn("pod msg decoded successfully")

	// match the pod number
	for {
		podState := shared.GetPodState()
		if PodVerifiedMsg.PodNumber == podState.LatestPodHeight {
			break
		}
		logs.Log.Warn("pod number is not matched, waiting for new pod")
		fmt.Println(PodVerifiedMsg.PodNumber, podState.LatestPodHeight)
		time.Sleep(3 * time.Second)
	}
	logs.Log.Warn("pod number matched successfully ")

	if PodVerifiedMsg.VerificationResult == true {
		logs.Log.Info("Saving current pod")
		saveVerifiedPOD() // save the latest pod details and make next pod
		logs.Log.Info("generating next pod")
		GenerateUnverifiedPods() // generate next pod

		return
	} else {
		logs.Log.Error("Pod verification failed, its time for voting")
		return
	}

}

//import (
//	"context"
//	"encoding/json"
//	"fmt"
//	"github.com/airchains-network/decentralized-sequencer/junction"
//	logs "github.com/airchains-network/decentralized-sequencer/log"
//	"github.com/airchains-network/decentralized-sequencer/node/shared"
//	"github.com/airchains-network/decentralized-sequencer/types"
//	"github.com/libp2p/go-libp2p/core/host"
//	"github.com/libp2p/go-libp2p/core/peer"
//	"github.com/rs/zerolog/log"
//	"math/rand"
//	"time"
//)
//
//func ProcessGossipMessage(node host.Host, ctx context.Context, dataType string, dataByte []byte, messageBroadcaster peer.ID) {
//	_ = node
//	switch dataType {
//	case "proof":
//		ProofHandler(ctx, dataByte, messageBroadcaster)
//		return
//	case "proofResult":
//		ProofResultHandler(dataByte, messageBroadcaster)
//		return
//	case "proofVoteResult":
//		ProofVoteResultHandler(dataByte, messageBroadcaster)
//		// VRF call
//		return
//	case "":
//		return
//	default:
//		return
//	}
//}
//
//// ProofHandler processes the proof received in a P2P message.
//func ProofHandler(ctx context.Context, dataByte []byte, messageBroadcaster peer.ID) {
//	var proofData ProofData
//	if err := json.Unmarshal(dataByte, &proofData); err != nil {
//		logs.Log.Info("Error unmarshaling proof: %v")
//		return
//	}
//
//	currentPodData := shared.GetPodState()
//	receivedTrackAppHash := proofData.TrackAppHash
//	receivedPodNumber := proofData.PodNumber
//
//	fmt.Println("local latest pod number: ", currentPodData.LatestPodHeight)
//	fmt.Println("received pod number:", receivedPodNumber)
//
//	// match pod numbers
//	if currentPodData.LatestPodHeight != receivedPodNumber {
//		// maybe proof may not be generated and its still in previous pod
//		if currentPodData.LatestPodHeight+1 == receivedPodNumber {
//			// insert it in MasterTrackAppHash
//			logs.Log.Info("Pod Number Is 1 behind current pod")
//			currentPodData.MasterTrackAppHash = receivedTrackAppHash
//			shared.SetPodState(currentPodData)
//			return
//		} else {
//			logs.Log.Warn("Pod Number Mismatch")
//			SendWrongPodNumberError(ctx, receivedPodNumber, messageBroadcaster)
//			return
//		}
//	} else {
//		logs.Log.Info("Current App Hash")
//		fmt.Println(currentPodData.TracksAppHash)
//		logs.Log.Info("Received App Hash")
//		fmt.Println(receivedTrackAppHash)
//
//		// match track app hash
//		if string(currentPodData.TracksAppHash) == string(receivedTrackAppHash) {
//			//if bytes.Equal(currentPodData.TracksAppHash, receivedTrackAppHash) {
//			logs.Log.Info("Tracks App Hash Matched. Sending Valid Proof Vote")
//			SendValidProof(ctx, receivedPodNumber, messageBroadcaster)
//			return
//		} else {
//			logs.Log.Warn("Tracks App Hash NOT Matched. Sending NOT Valid Proof Vote")
//			SendInvalidProofError(ctx, receivedPodNumber, messageBroadcaster)
//			return
//		}
//	}
//
//}
//
//func ProofResultHandler(dataByte []byte, messageBroadcaster peer.ID) {
//
//	var proofResult ProofResult
//	err := json.Unmarshal(dataByte, &proofResult)
//	if err != nil {
//		log.Error().Msg("Error Unmarshalling Proof Result: %v")
//		return
//	}
//
//	// update pod state votes based on proof result
//	updatePodStateVotes(proofResult, messageBroadcaster)
//
//	// count votes of all nodes, if 2/3 votes are true, then
//	voteResult, isVotesEnough := calculateVotes()
//
//	// if votes are enough
//	if isVotesEnough {
//		// if votes are enough and 2/3 votes are true
//		if voteResult {
//			// if all votes are done and pod match, then start pod submission process: 1. init VRF, 2. verify VRN, 3. submit pod, 4. verify pod
//			PodNumber := int(shared.GetPodState().LatestPodHeight)
//			peers := getAllPeers(Node)
//			upperBond := uint64(len(peers))
//			success, addr := junction.InitVRF(upperBond)
//			if !success {
//				logs.Log.Error("Failed to Init VRF")
//				return
//			}
//			logs.Log.Info("VRF initiated")
//
//			// choose one verifiable random node to verify the VRF
//			rand.Seed(time.Now().UnixNano())
//			selectedNode := peers[rand.Uint64()%(upperBond+1)]
//			// todo make selected node verifiable, currently its random
//
//			// send verify VRF message to selected node
//			VRFInitiatedMsg := VRFInitiatedMsg{
//				PodNumber:           uint64(PodNumber),
//				SelectedNode:        selectedNode.ID,
//				VrfInitiatorAddress: addr,
//			}
//
//			VRFInitiatedMsgByte, err := json.Marshal(VRFInitiatedMsg)
//			if err != nil {
//				logs.Log.Error("Error in Marshaling ProofVote Result")
//				return
//			}
//			gossipMsg := types.GossipData{
//				Type: "verifyVrf",
//				Data: VRFInitiatedMsgByte,
//			}
//			gossipMsgByte, err := json.Marshal(gossipMsg)
//			if err != nil {
//				logs.Log.Error("Error marshaling gossip message")
//				return
//			}
//			BroadcastMessage(CTX, Node, gossipMsgByte)
//
//
//			//DaData := shared.GetPodState().Batch.TransactionHash
//			//daDataByte := []byte{}
//			//for _, str := range DaData {
//			//	daDataByte = append(daDataByte, []byte(str)...)
//			//}
//			//ZkProof := shared.GetPodState().LatestPodProof
//			//
//			//finalizeDA := types.FinalizeDA{
//			//	CompressedHash: DaData,
//			//	Proof:          ZkProof,
//			//	PodNumber:      PodNumber,
//			//}
//			//
//			//_, err := json.Marshal(finalizeDA)
//			//if err != nil {
//			//	log.Printf("Error marshaling da data: %v", err)
//			//}
//
//
//
//			//success = junction.ValidateVRF(serializedRC)
//			//if !success {
//			//	logs.Log.Error("Failed to Init VRF")
//			//	return
//			//}
//			//logs.Log.Info("validate vrf success")
//			//
//			//// check if VRF is successfully validated
//			//var vrfRecord *junctionTypes.VrfRecord
//			//vrfRecord = junction.QueryVRF()
//			//if vrfRecord == nil {
//			//	logs.Log.Error("VRF record is nil")
//			//	return
//			//}
//			//if !vrfRecord.IsVerified {
//			//	logs.Log.Error("Verification of VRF is failed, need Voting for correct VRN")
//			//	return
//			//}
//			//
//			//// DA submit
//			//connection := shared.Node.NodeConnections
//			//mdb := connection.GetDataAvailabilityDatabaseConnection()
//			//dbName, err := mock.MockDA(mdb, daDataByte, PodNumber) // (mockda-%d", batchNumber), nil
//			//if err != nil {
//			//	logs.Log.Error("Error in submitting data to DA")
//			//	return
//			//}
//			//_ = dbName
//			//logs.Log.Info("data in DA submitted")
//
//			// submit pod
//			//success = junction.SubmitCurrentPod()
//			//if !success {
//			//	logs.Log.Error("Failed to submit pod")
//			//	return
//			//}
//			//logs.Log.Info("pod submitted")
//			//
//			//// verify pod
//			//junction.VerifyCurrentPod()
//			//if !success {
//			//	logs.Log.Error("Failed to Transact Verify pod")
//			//	return
//			//}
//			//logs.Log.Info("pod verification transaction done")
//			//// todo : query if verification return true or false...
//
//			// Send Message containing the Da Hash and Junction Hash to the respective nodes
//			//saveVerifiedPOD()
//			//peerListLocked = false
//			//peerListLock.Unlock()
//			//peerListLock.Lock()
//			//for _, peerInfo := range incomingPeers.GetPeers() {
//			//	peerList.AddPeer(peerInfo)
//			//}
//			//peerListLock.Unlock() // save data to database
//			//GenerateUnverifiedPods() // generate next pod
//
//		} else {
//			// TODO
//		}
//	}
//}
//
//func SendWrongPodNumberError(ctx context.Context, podNumber uint64, messageBroadcaster peer.ID) {
//
//	logs.Log.Error("Wrong Pods Number Receieved Cannot Process Proof")
//
//	ProofResult := ProofResult{
//		PodNumber: podNumber,
//		Success:   false,
//	}
//
//	ProofResultByte, err := json.Marshal(ProofResult)
//	if err != nil {
//		logs.Log.Error("Error in Marshaling Proof Result")
//		return
//	}
//
//	gossipMsg := types.GossipData{
//		Type: "proofResult",
//		Data: ProofResultByte,
//	}
//	gossipMsgByte, err := json.Marshal(gossipMsg)
//	if err != nil {
//		logs.Log.Error("Error marshaling gossip message")
//		return
//	}
//
//	err = sendMessage(ctx, Node, messageBroadcaster, gossipMsgByte)
//	if err != nil {
//		return
//	}
//
//}
//
//func SendInvalidProofError(ctx context.Context, podNumber uint64, messageBroadcaster peer.ID) {
//
//	logs.Log.Error("Tracks App Hash  Recieved Doesnt Match with the Generated Track App Hash")
//
//	ProofResult := ProofResult{
//		PodNumber: podNumber,
//		Success:   false,
//	}
//
//	ProofResultByte, err := json.Marshal(ProofResult)
//	if err != nil {
//		logs.Log.Error("Error in Marshaling Proof Result")
//		return
//	}
//
//	gossipMsg := types.GossipData{
//		Type: "proofResult",
//		Data: ProofResultByte,
//	}
//	gossipMsgByte, err := json.Marshal(gossipMsg)
//	if err != nil {
//		logs.Log.Error("Error marshaling gossip message")
//		return
//	}
//
//	sendMessage(ctx, Node, messageBroadcaster, gossipMsgByte)
//}
//
//func SendValidProof(ctx context.Context, podNumber uint64, messageBroadcaster peer.ID) {
//	logs.Log.Info("Proof Validated Successfully")
//
//	ProofResult := ProofResult{
//		PodNumber: podNumber,
//		Success:   true,
//	}
//
//	ProofResultByte, err := json.Marshal(ProofResult)
//	if err != nil {
//		logs.Log.Error("Error in Marshaling Proof Result")
//		return
//	}
//
//	gossipMsg := types.GossipData{
//		Type: "proofResult",
//		Data: ProofResultByte,
//	}
//	gossipMsgByte, err := json.Marshal(gossipMsg)
//	if err != nil {
//		logs.Log.Error("Error marshaling gossip message")
//		return
//	}
//
//	sendMessage(ctx, Node, messageBroadcaster, gossipMsgByte)
//}
//
//func updatePodStateVotes(proofResult ProofResult, nodeId peer.ID) {
//	// Logic to update the pod state votes based on the proof result
//	podState := shared.GetPodState()
//
//	currentVotes := podState.Votes
//
//	// check if the vote already exists
//	for _, vote := range currentVotes {
//		if vote.PeerID == nodeId.String() {
//			// vote already exists
//			return
//		}
//	}
//
//	// add new vote to the currentVotes
//	currentVotes[nodeId.String()] = shared.Votes{
//		PeerID: nodeId.String(),
//		Vote:   proofResult.Success,
//	}
//
//	podState.Votes = currentVotes
//
//	shared.SetPodState(podState)
//
//	// calcualte votes
//}
//
//func calculateVotes() (voteResult, isVotesEnough bool) {
//
//	podState := shared.GetPodState()
//	allVotes := podState.Votes
//
//	currentVotesCount := len(allVotes)
//	peers := getAllPeers(Node)
//
//	// if all peers have voted
//	// TODO: do it even if all peers have not voted, and then also 2/3 returned `true`, then do this:
//	if currentVotesCount == len(peers) {
//
//		// count votes of all nodes, if 2/3 votes are true
//
//		trueVotes := 0
//		falseVotes := 0
//
//		for _, vote := range allVotes {
//			if vote.Vote {
//				trueVotes++
//			} else {
//				falseVotes++
//			}
//		}
//
//		peerLen := len(peers)
//		trueVotePercentage := (float64(trueVotes) / float64(peerLen)) * 100
//
//		voteResult := VoteResult{
//			TrueCount:          trueVotes,
//			FalseCount:         falseVotes,
//			TrueVotePercentage: trueVotePercentage,
//			Votes:              allVotes,
//			Success:            false,
//		}
//
//		// if 2/3 votes are true
//		if trueVotePercentage >= 66.67 {
//			voteResult.Success = true
//		}
//
//		sendPodVoteResultToAllPeers(voteResult)
//
//		if voteResult.Success {
//			return true, true
//		} else {
//			return false, true
//		}
//
//	}
//	return false, false
//}
//
//func sendPodVoteResultToAllPeers(voteResult VoteResult) {
//	// send success result to all peers
//	// and update the pod state
//	logs.Log.Info("Proof Validated Successfully")
//
//	ProofVoteResultByte, err := json.Marshal(voteResult)
//	if err != nil {
//		logs.Log.Error("Error in Marshaling ProofVote Result")
//		return
//	}
//
//	gossipMsg := types.GossipData{
//		Type: "proofVoteResult",
//		Data: ProofVoteResultByte,
//	}
//	gossipMsgByte, err := json.Marshal(gossipMsg)
//	if err != nil {
//		logs.Log.Error("Error marshaling gossip message")
//		return
//	}
//
//	BroadcastMessage(CTX, Node, gossipMsgByte)
//}
//
//func ProofVoteResultHandler(dataByte []byte, messageBroadcaster peer.ID) {
//	var voteResult VoteResult
//	err := json.Unmarshal(dataByte, &voteResult)
//	if err != nil {
//		panic("error in unmarshling proof vote result")
//	}
//
//	fmt.Println("Proof Vote Result Received: ", voteResult)
//
//	if voteResult.Success {
//
//		//saveVerifiedPOD() // save data to database
//		//peerListLocked = false
//		//peerListLock.Unlock()
//		//
//		//peerListLock.Lock()
//		//for _, peerInfo := range incomingPeers.GetPeers() {
//		//	peerList.AddPeer(peerInfo)
//		//}
//		//peerListLock.Unlock()
//		//GenerateUnverifiedPods() // generate next pod
//	} else {
//		logs.Log.Error("Proof Validation Failed, I am stopping here.. dont know what to do ....")
//		// don't know what to do yet
//	}
//}
//
//// Unused functions
//
//// updatePodState updates the pod's state based on the proof data received.
//func updatePodState(proofData ProofData) {
//	currentPodData := shared.GetPodState()
//	currentPodData.LatestPodHeight = 1000000 // Example modification, should be based on actual proof data
//	shared.SetPodState(currentPodData)
//}
//
//// createProofResult creates a proof result based on the proof data received.
//func createProofResult(proofData ProofData) ProofResult {
//	// Logic to determine the success or failure of the proof validation
//	return ProofResult{
//		PodNumber: proofData.PodNumber,
//		Success:   true, // This should be determined by actual validation logic
//	}
//}
//
//// sendProofResult marshals and sends the proof result to the P2P network.
//func sendProofResult(ctx context.Context, node host.Host, proofResult ProofResult) {
//	proofResultByte, err := json.Marshal(proofResult)
//	if err != nil {
//		log.Printf("Error marshaling proof result: %v", err)
//		return
//	}
//
//	gossipMsg := types.GossipData{
//		Type: "proofResult",
//		Data: proofResultByte,
//	}
//
//	gossipMsgByte, err := json.Marshal(gossipMsg)
//	if err != nil {
//		log.Printf("Error marshaling gossip message: %v", err)
//		return
//	}
//
//	log.Printf("Sending proof result: %s", gossipMsgByte)
//	BroadcastMessage(ctx, node, gossipMsgByte)
//}
