package servers

import (
	"log"
	"rb3server/database"
	"rb3server/quazal"
	"rb3server/utils"

	"github.com/ihatecompvir/nex-go"
)

const maxStationURLLength = 1024

// ValidateStationURLs does some checks on station URLs on returns whether or not they should be considered valid
// TODO: verify the correctness and etc. like they must start with prudp:// or prudps:// and what not
func ValidateStationURLs(stationURLs ...string) bool {
	for _, stationURL := range stationURLs {
		if len(stationURL) > maxStationURLLength {
			return false
		}
	}
	return true
}

// ValidateClientPID checks if the client has a valid, unbanned non-Master PID
func ValidateNonMasterClientPID(server *nex.Server, client *nex.Client, callID uint32, protocolId int) (bool, error) {
	// Check that the claimed PID has logged in
	hasLoggedIn, err := utils.GetClientStoreSingleton().IsValidPID(client.Address().String(), client.PlayerID())
	if err != nil {
		log.Println("Error checking PID validity: ", err)
		SendErrorCode(server, client, uint8(protocolId), callID, quazal.OperationError)
		return false, err
	}

	if !hasLoggedIn || client.PlayerID() == 0 || database.IsPIDAMasterUser(int(client.PlayerID())) || database.IsPIDBanned(int(client.PlayerID())) {
		log.Println("Client is attempting to perform a privileged action without a valid server-assigned PID, rejecting call")
		SendErrorCode(server, client, uint8(protocolId), callID, quazal.NotAuthenticated)
		return false, nil
	}

	return true, nil
}

// ValidateClientPID checks if the client has a valid PID, Master User PIDs allowed
func ValidateClientPID(server *nex.Server, client *nex.Client, callID uint32, protocolId int) (bool, error) {
	// Check that the claimed PID has logged in
	hasLoggedIn, err := utils.GetClientStoreSingleton().IsValidPID(client.Address().String(), client.PlayerID())
	if err != nil {
		log.Println("Error checking PID validity: ", err)
		SendErrorCode(server, client, uint8(protocolId), callID, quazal.OperationError)
		return false, err
	}

	if !hasLoggedIn || client.PlayerID() == 0 {
		log.Println("Client is attempting to perform a privileged action without a valid server-assigned PID, rejecting call")
		SendErrorCode(server, client, uint8(protocolId), callID, quazal.NotAuthenticated)
		return false, nil
	}

	return true, nil
}
