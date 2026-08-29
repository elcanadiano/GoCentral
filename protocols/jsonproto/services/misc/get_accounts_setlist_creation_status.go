package misc

import (
	"context"
	"log"
	"rb3server/protocols/jsonproto/marshaler"

	"github.com/ihatecompvir/nex-go"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type SetlistCreationStatusRequest struct {
	Region      string `json:"region"`
	SystemMS    int    `json:"system_ms"`
	MachineID   string `json:"machine_id"`
	SessionGUID string `json:"session_guid"`
	PID         int    `json:"pid"`
}

type SetlistCreationStatusResponse struct {
	PID     int `json:"pid"`
	Creator int `json:"creator"`
}

type SetlistCreationStatusService struct {
}

func (service SetlistCreationStatusService) Path() string {
	return "misc/get_accounts_setlist_creation_status"
}

func (service SetlistCreationStatusService) Handle(data string, database *mongo.Database, client *nex.Client) (string, error) {
	var req SetlistCreationStatusRequest
	err := marshaler.UnmarshalRequest(data, &req)
	if err != nil {
		return "", err
	}

	if req.PID != int(client.PlayerID()) {
		log.Println("Client-supplied PID did not match server-assigned PID, rejecting setlist creation request")
		return "", err
	}

	setlistsCollection := database.Collection("setlists")
	count, err := setlistsCollection.CountDocuments(context.TODO(), bson.M{"pid": req.PID, "type": bson.M{"$nin": []int{1000, 1001, 1002}}})
	if err != nil {
		log.Printf("Could not count setlists for PID %d: %v\n", req.PID, err)
		return "", err
	}

	creator := 0
	if count > 0 {
		creator = 1
	}

	res := []SetlistCreationStatusResponse{{
		req.PID,
		creator,
	}}

	return marshaler.MarshalResponse(service.Path(), res)
}
