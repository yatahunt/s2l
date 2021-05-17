package main

import (
	log "bitbucket.org/aisee/minilog"
	"github.com/aiseeq/s2l/protocol/api"
	"github.com/aiseeq/s2l/protocol/client"
	"github.com/aiseeq/s2l/protocol/enums/ability"
)

func main() {
	client.SetMap("DeathAura506.SC2Map") // client.Random1v1Map()
	client.SetRealtime()
	// Play a random map against a medium difficulty computer
	bot := client.NewParticipant(api.Race_Protoss, "ProbeRush")
	cpu := client.NewComputer(api.Race_Protoss, api.Difficulty_Medium, api.AIBuild_RandomBuild)

	if !client.LoadSettings() {
		return
	}
	var config *client.GameConfig
	if client.LadderGamePort > 0 {
		// Game against other bot or human via Ladder Manager
		config = client.NewGameConfig(bot)
		log.Info("Connecting to port ", client.LadderGamePort)
		config.Connect(client.LadderGamePort)
		config.SetupPorts(client.LadderStartPort)
		config.JoinGame()
		log.Info("Successfully joined game")
	} else {
		// Local game versus cpu
		config = client.NewGameConfig(bot, cpu)
		config.LaunchStarcraft()
		config.StartGame(client.MapPath())
	}

	c := config.Clients[0]
	gameInfo, err := c.Connection.GameInfo()
	if err != nil {
		log.Fatal(err)
	}
	enemyStartLocation := gameInfo.StartRaw.StartLocations[0]

	obs, err := c.Connection.Observation(api.RequestObservation{})
	if err != nil {
		log.Fatal(err)
	}
	var tags []api.UnitTag
	for _, unit := range obs.Observation.RawData.Units {
		tags = append(tags, unit.Tag)
	}
	resp, err := c.Connection.Action(
		api.RequestAction{
			Actions: []*api.Action{{
				ActionRaw: &api.ActionRaw{
					Action: &api.ActionRaw_UnitCommand{
						UnitCommand: &api.ActionRawUnitCommand{
							AbilityId: ability.Attack_Attack,
							UnitTags:  tags,
							Target: &api.ActionRawUnitCommand_TargetWorldSpacePos{
								TargetWorldSpacePos: enemyStartLocation,
							}}}}}},
		},
	)
	if err != nil {
		log.Fatal(err)
	}
	log.Info(resp)

	for c.Connection.Status == api.Status_in_game {
		if resp, err := c.Connection.Step(api.RequestStep{Count: 1}); err != nil {
			log.Fatal(err)
		} else {
			log.Info(resp)
			obs, err := c.Connection.Observation(api.RequestObservation{})
			if err != nil {
				log.Fatal(err)
			}
			log.Info(obs.Observation.GameLoop)
		}
	}
}
