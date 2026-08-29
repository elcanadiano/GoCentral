package band

import (
	"context"
	"log"
	"rb3server/models"
	"rb3server/protocols/jsonproto/marshaler"
	"strings"

	"github.com/ihatecompvir/nex-go"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type BandNameCheckRequest struct {
	Name        string `json:"name"`
	Region      string `json:"region"`
	Flags       int    `json:"flags"`
	PID         int    `json:"pid"`
	SystemMS    int    `json:"system_ms"`
	MachineID   string `json:"machine_id"`
	SessionGUID string `json:"session_guid"`
}

type BandNameCheckResponse struct {
	RetCode int `json:"ret_code"`
}

type BandNameCheckService struct {
}

func (service BandNameCheckService) Path() string {
	return "entities/band/name/check"
}

func (service BandNameCheckService) Handle(data string, database *mongo.Database, client *nex.Client) (string, error) {
	var req BandNameCheckRequest
	err := marshaler.UnmarshalRequest(data, &req)
	if err != nil {
		return "", err
	}

	var config models.Config
	configCollection := database.Collection("config")
	err = configCollection.FindOne(context.TODO(), bson.M{}).Decode(&config)
	if err != nil {
		log.Printf("Could not get config %v\n", err)
	}

	for _, profanity := range config.ProfanityList {
		if profanity != "" && req.Name != "" && len(req.Name) >= len(profanity) {
			lowerName := strings.ToLower(req.Name)
			lowerProfanity := strings.ToLower(profanity)
			if lowerName == lowerProfanity || strings.Contains(lowerName, lowerProfanity) {
				return marshaler.MarshalResponse(service.Path(), []BandNameCheckResponse{{2}})
			}
		}
	}

	return marshaler.MarshalResponse(service.Path(), []BandNameCheckResponse{{1}})
}
