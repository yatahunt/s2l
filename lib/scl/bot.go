package scl

import (
	"bitbucket.org/aisee/minilog"
	"github.com/aiseeq/s2l/lib/actions"
	"github.com/aiseeq/s2l/lib/grid"
	"github.com/aiseeq/s2l/lib/point"
	"github.com/aiseeq/s2l/protocol/api"
	"github.com/aiseeq/s2l/protocol/client"
	"github.com/aiseeq/s2l/protocol/enums/protoss"
	"github.com/aiseeq/s2l/protocol/enums/terran"
	"github.com/aiseeq/s2l/protocol/enums/zerg"
	"math"
	"os"
)

type Bot struct {
	Client        *client.Client
	Obs           *api.Observation
	Data          *api.ResponseData
	Info          *api.ResponseGameInfo
	Chat          []*api.ChatReceived
	Result        []*api.PlayerResult
	Errors        []*api.ActionError
	Actions       actions.Actions
	Cmds          *CommandsStack
	DebugCommands []*api.DebugCommand
	RecentEffects [][]*api.Effect // This needed because corrosive biles disappear from effects to early

	Locs struct {
		MapCenter       point.Point
		MyStart         point.Point
		MyStartMinVec   point.Point
		EnemyStart      point.Point
		EnemyStarts     point.Points
		EnemyMainCenter point.Point
		MyExps          point.Points
		EnemyExps       point.Points
	}
	Ramps struct {
		All   []Ramp
		My    Ramp
		Enemy Ramp
	}
	Units struct {
		My       UnitsByTypes
		MyAll    Units
		Enemy    UnitsByTypes
		AllEnemy UnitsByTypes
		Minerals UnitsByTypes
		Geysers  UnitsByTypes
		Neutral  UnitsByTypes
		ByTag    map[api.UnitTag]*Unit
	}
	Enemies struct {
		All      Units
		AllReady Units
		Visible  Units
		Clusters []*Cluster // Not used yet
	}
	U struct { // Moved from globals in units
		Types              []*api.UnitTypeData
		GroundAttackCircle map[api.UnitTypeID]point.Points
		Upgrades           []*api.UpgradeData
		Effects            []*api.EffectData
		UnitCost           map[api.UnitTypeID]Cost
		AbilityCost        map[api.AbilityID]Cost
		AbilityUnit        map[api.AbilityID]api.UnitTypeID
		UnitAbility        map[api.UnitTypeID]api.AbilityID
		UnitAliases        Aliases
		UnitsOrders        map[api.UnitTag]UnitOrder

		Attributes  map[api.UnitTypeID]map[api.Attribute]bool
		Weapons     map[api.UnitTypeID]Weapon
		HitsHistory map[api.UnitTag][]int
		PrevUnits   map[api.UnitTag]*Unit
		AfterAttack AttackDelays
		LastAttack  map[api.UnitTag]int
	}
	Miners struct {
		CCForMiner       map[api.UnitTag]api.UnitTag
		GasForMiner      map[api.UnitTag]api.UnitTag
		MineralForMiner  map[api.UnitTag]api.UnitTag
		TargetForMineral map[api.UnitTag]point.Point
		LastSeen         map[api.UnitTag]int
	}

	Grid           *grid.Grid
	SafeGrid       *grid.Grid
	ReaperGrid     *grid.Grid
	ReaperSafeGrid *grid.Grid
	// HomePaths        Steps
	// HomeReaperPaths  Steps
	// ExpPaths         []Steps
	WayMap           WaypointsMap
	SafeWayMap       WaypointsMap
	ReaperExists     bool
	ReaperWayMap     WaypointsMap
	ReaperSafeWayMap WaypointsMap

	EnemyRace       api.Race
	EnemyProduction TagsByTypes
	Orders          map[api.AbilityID]int
	FramesPerOrder  int
	Groups          *Groups
	MaxGroup        GroupID
	Upgrades        map[api.AbilityID]bool

	Loop             int
	LastLoop         int
	Minerals         int
	MineralsPerFrame float64
	Vespene          int
	VespenePerFrame  float64
	FoodCap          int
	FoodUsed         int
	FoodLeft         int

	UnitCreatedCallback func(unit *Unit)
}

var B *Bot // Pointer to the last created bot. It should be the only global here

const FPS = 22.4
const HitHistoryLoops = 56 // 2.5 sec
const ResourceSpreadDistance = 9
const minRampSize = 10
const airSpeedBoostRange = 5
const samePoint = 0.1
const KD8Radius = 1.75

func (b *Bot) UpdateObservation() {
	o, err := b.Client.Observation(api.RequestObservation{})
	if err != nil {
		log.Error(err)
		return
	}
	b.Obs = o.Observation
	b.Chat = o.Chat
	b.Result = o.PlayerResult
	b.Errors = o.ActionErrors
	// todo: Action
}

func (b *Bot) UpdateData() {
	data, err := b.Client.Data(api.RequestData{
		AbilityId:  true,
		UnitTypeId: true,
		UpgradeId:  true,
		BuffId:     true,
		EffectId:   true,
	})
	if err != nil {
		log.Error(err)
		return
	}
	b.Data = data
}

func (b *Bot) UpdateInfo() {
	info, err := b.Client.GameInfo()
	if err != nil {
		log.Error(err)
		return
	}
	b.Info = info
}

func New(client *client.Client, ucc func(unit *Unit)) *Bot {
	b := Bot{}
	b.Client = client
	b.UnitCreatedCallback = ucc
	b.Cmds = &CommandsStack{}
	B = &b

	return &b
}

func (b *Bot) Init(stop <-chan struct{}) {
	// Init unit data
	b.U.Types = []*api.UnitTypeData{}
	b.U.GroundAttackCircle = map[api.UnitTypeID]point.Points{}
	b.U.Upgrades = []*api.UpgradeData{}
	b.U.Effects = []*api.EffectData{}
	b.U.UnitCost = map[api.UnitTypeID]Cost{}
	b.U.AbilityCost = map[api.AbilityID]Cost{}
	b.U.AbilityUnit = map[api.AbilityID]api.UnitTypeID{}
	b.U.UnitAbility = map[api.UnitTypeID]api.AbilityID{}
	b.U.UnitAliases = Aliases{}
	b.U.UnitsOrders = map[api.UnitTag]UnitOrder{}

	b.U.Attributes = map[api.UnitTypeID]map[api.Attribute]bool{}
	b.U.Weapons = map[api.UnitTypeID]Weapon{}
	b.U.HitsHistory = map[api.UnitTag][]int{}
	b.U.PrevUnits = map[api.UnitTag]*Unit{}

	// Проблема в том, что есть большая разница между выстрелом после разворота и выстрелом без него
	// todo: как-то учитывать начальное направление взгляда юнита?
	b.U.AfterAttack = AttackDelays{
		terran.AutoTurret:    9,
		terran.Banshee:       6,
		terran.Cyclone:       6,
		terran.Hellion:       6,
		terran.HellionTank:   6,
		terran.SiegeTank:     6,
		terran.Reaper:        6, // todo: всё равно иногда не достаточно (редко)
		terran.SCV:           9,
		terran.Thor:          24, // todo: он может двигаться быстрее, если была воздушная атака
		terran.ThorAP:        24,
		terran.VikingFighter: 6,
		zerg.Drone:           6,
		zerg.Queen:           6,
		protoss.Probe:        6,
		protoss.Stalker:      6,
	}
	B.U.LastAttack = map[api.UnitTag]int{}

	b.UpdateObservation()
	b.UpdateData()
	b.UpdateInfo()

	b.InitUnits(b.Data.Units)
	b.InitUpgrades(b.Data.Upgrades)
	b.InitEffects(b.Data.Effects)
	b.ParseUnits()
	b.ParseOrders()
	b.InitLocations()
	b.FindExpansions()
	b.FindRamps()
	b.InitRamps()
	go b.RenewPaths(stop)
}

func (b *Bot) AddToCluster(enemy *Unit, c *Cluster) {
	c.Units[enemy] = struct{}{}
	c.Food += float64(b.U.Types[enemy.UnitType].FoodRequired)
	enemy.Cluster = c

	for _, u := range enemy.Neighbours {
		if u.Cluster != nil {
			continue
		}

		b.AddToCluster(u, c)
	}
}

func (b *Bot) FindClusters() {
	enemies := b.Enemies.AllReady.Filter(func(unit *Unit) bool {
		return !unit.IsWorker() && (unit.IsDefensive() || !unit.IsStructure())
	})
	/*enemies := b.Units.MyAll.Filter(func(unit *Unit) bool {
		return unit.IsReady() && !unit.IsWorker() && (unit.IsDefensive() || !unit.IsStructure())
	})*/
	// Find neighbours for each unit
	for _, u := range enemies {
		u.Neighbours = enemies.Filter(func(unit *Unit) bool {
			if u == unit {
				return false
			}

			r1 := math.Max(u.GroundRange(), u.AirRange())
			r2 := math.Max(unit.GroundRange(), unit.AirRange())
			r := math.Max(r1, r2) + 2
			return unit.IsCloserThan(r, u)
		})
	}

	// Add units connected by neighbourship to clusters
	b.Enemies.Clusters = []*Cluster{}
	for _, enemy := range enemies {
		enemy.Cluster = nil // Remove cluster for old enemies (not visible now)
	}
	for _, enemy := range enemies {
		if enemy.Cluster != nil {
			continue
		}

		c := &Cluster{Units: UnitsMap{}}
		b.AddToCluster(enemy, c)
		b.Enemies.Clusters = append(b.Enemies.Clusters, c)
	}
}

func (b *Bot) ParseUnits() {
	// Restore default data
	if b.Grid == nil {
		b.Grid = grid.New(b.Info.StartRaw, b.Obs.RawData.MapState)
	} else {
		// I need to renew it because it could be locked somewhere else
		b.Grid.Renew(b.Info.StartRaw, b.Obs.RawData.MapState)
	}
	b.Grid.Lock()

	b.Units.My = UnitsByTypes{}
	b.Units.Minerals = UnitsByTypes{}
	b.Units.Geysers = UnitsByTypes{}
	b.Units.Neutral = UnitsByTypes{}
	b.Units.Enemy = UnitsByTypes{}
	b.Units.ByTag = map[api.UnitTag]*Unit{}
	if b.Groups == nil {
		b.Groups = NewGroups(b.MaxGroup)
	} else {
		b.Groups.ClearUnits()
	}
	b.Units.AllEnemy = UnitsByTypes{}

	for _, unit := range b.Obs.RawData.Units {
		u, isNew := b.NewUnit(unit)
		b.Units.ByTag[u.Tag] = u
		switch unit.Alliance {
		case api.Alliance_Self:
			b.Units.My.Add(unit.UnitType, u)
			b.Groups.Fill(u)
			if isNew && b.UnitCreatedCallback != nil {
				b.UnitCreatedCallback(u)
			}
		case api.Alliance_Enemy:
			b.Units.Enemy.Add(unit.UnitType, u)
			b.Units.AllEnemy.Add(unit.UnitType, u)
			b.EnemyProduction.Add(unit.UnitType, unit.Tag) // Used to count score to decide what unit to build
		case api.Alliance_Neutral:
			if u.IsMineral() {
				b.Units.Minerals.Add(unit.UnitType, u)
			} else if u.IsGeyser() {
				b.Units.Geysers.Add(unit.UnitType, u)
			} else {
				b.Units.Neutral.Add(unit.UnitType, u)
			}
		default:
			log.Error("Not supported alliance: ", unit)
			continue
		}

		// Modify pathing and building maps
		if u.IsStructure() && !u.IsFlying {
			if u.Alliance == api.Alliance_Self || u.Alliance == api.Alliance_Enemy {
				var size BuildingSize = 0
				pos := u.Point()
				switch {
				case u.Radius <= 1:
					// Nothing
				case u.Radius >= 1.125 && u.Radius <= 1.25:
					size = S2x2
					pos -= point.Pt(1, 1)
				case u.Radius > 1.25 && u.Radius < 2.75:
					size = S3x3
				case u.Radius == 2.75:
					size = S5x5
				default:
					log.Warning("No size for building:", u.UnitType, u.Radius)
				}
				if size != 0 {
					for _, p := range b.GetBuildingPoints(pos, size) {
						b.Grid.SetBuildable(p, false)
						if u.UnitType != terran.SupplyDepotLowered {
							b.Grid.SetPathable(p, false)
						}
					}
				}
			} else { // api.Alliance_Neutral
				var size BuildingSize = 0
				var unpathable bool
				pos := u.Point()
				if u.IsMineral() {
					size = S2x1
					pos -= 1
					unpathable = true
				} else {
					size = DestructibleSize[u.UnitType]
					switch size {
					case UnbuildableRocks:
						pos -= 1 + 1i
					case BreakableRocks2x2:
						pos -= 1 + 1i
						unpathable = true
					case BreakableRocks4x4:
						unpathable = true
					case BreakableRocks4x2:
						unpathable = true
					case BreakableRocks2x4:
						unpathable = true
					case BreakableRocks6x2:
						unpathable = true
					case BreakableRocks2x6:
						unpathable = true
					case BreakableRocks6x6:
						unpathable = true
					case BreakableRocksDiagBLUR:
						unpathable = true
					case BreakableRocksDiagULBR:
						unpathable = true
					case BreakableHorizontalHuge:
						unpathable = true
					case BreakableVerticalHuge:
						unpathable = true
					default:
						// log.Info(u.UnitType)
					}
				}
				if size != 0 {
					for _, p := range b.GetBuildingPoints(pos, size) {
						b.Grid.SetBuildable(p, false)
						if unpathable {
							b.Grid.SetPathable(p, false)
						}
					}
				}
			}
		}
	}
	b.Grid.Unlock()
	b.ReaperExists = b.Units.My[terran.Reaper].Exists()

	for _, u := range b.Enemies.All { // old enemy units
		if b.Units.ByTag[u.Tag] != nil {
			continue // Unit already added
		}
		visible := true
		h := b.Grid.HeightAt(u)
		// Iterate unit's position and points around it
		for _, p := range append([]point.Point{u.Point()}, u.Point().Neighbours4(1)...) {
			if !b.Grid.IsVisible(p) && b.Grid.HeightAt(p) == h && b.Grid.IsPathable(p) {
				visible = false
				break
			}
		}
		if visible {
			continue // Old position is scouted, but there is no unit, skip it
		}
		u.DisplayType = api.DisplayType_Snapshot
		b.Units.AllEnemy.Add(u.UnitType, u)
	}

	b.Units.MyAll = B.Units.My.All()                 // All my units
	b.Enemies.All = b.Units.AllEnemy.All()           // All enemy units including those that are not visible now
	b.Enemies.AllReady = b.Enemies.All.Filter(Ready) // Same but filter ready only
	b.Enemies.Visible = b.Units.Enemy.All()          // All enemy units that are currently visible

	b.RequestAvailableAbilities(false, b.Units.MyAll...)
	b.RequestAvailableAbilities(true, b.Units.MyAll...)
}

func (b *Bot) ParseOrders() {
	b.Orders = map[api.AbilityID]int{}
	for _, unit := range b.Units.MyAll {
		for _, order := range unit.Orders {
			b.Orders[order.AbilityId]++
		}
	}
}

func (b *Bot) ParseObservation() {
	b.Loop = int(b.Obs.GameLoop)
	b.Minerals = int(b.Obs.PlayerCommon.Minerals)
	b.Vespene = int(b.Obs.PlayerCommon.Vespene)
	b.FoodCap = int(b.Obs.PlayerCommon.FoodCap)
	b.FoodUsed = int(b.Obs.PlayerCommon.FoodUsed)
	b.FoodLeft = b.FoodCap - b.FoodUsed
	b.MineralsPerFrame = float64(b.Obs.Score.ScoreDetails.CollectionRateMinerals) / 60 / 22.4
	b.VespenePerFrame = float64(b.Obs.Score.ScoreDetails.CollectionRateVespene) / 60 / 22.4
	b.Upgrades = map[api.AbilityID]bool{}
	if b.U.Upgrades != nil {
		for _, uid := range b.Obs.RawData.Player.UpgradeIds {
			b.Upgrades[b.U.Upgrades[uid].AbilityId] = true
		}
	}
	b.RecentEffects = append(b.RecentEffects, B.Obs.RawData.Effects) // Fixes corosive biles early disappearence
	if len(b.RecentEffects) > 21/b.FramesPerOrder {                  // 21 frames
		b.RecentEffects = b.RecentEffects[1:]
	}
}

func (b *Bot) DetectEnemyRace() {
	if b.EnemyRace == api.Race_NoRace {
		enemyId := 3 - b.Obs.PlayerCommon.PlayerId // hack?
		b.EnemyRace = b.Info.PlayerInfo[enemyId-1].RaceRequested
	} else if b.EnemyRace == api.Race_Random && b.Units.Enemy.Exists() {
		unit := b.Enemies.Visible.First()
		b.EnemyRace = b.U.Types[unit.UnitType].Race
	}
}

func (b *Bot) UnitTargetPos(u *Unit) point.Point {
	pos := u.TargetPos()
	if pos != 0 {
		return pos
	}
	enemy := b.Enemies.Visible.ByTag(u.TargetTag())
	if enemy != nil {
		return enemy.Point()
	}
	return 0
}

func (b *Bot) RequestAvailableAbilities(irr bool, us ...*Unit) {
	var rqaas []*api.RequestQueryAvailableAbilities
	for _, u := range us {
		rqaas = append(rqaas, &api.RequestQueryAvailableAbilities{UnitTag: u.Tag})
	}
	resp, err := b.Client.Query(api.RequestQuery{Abilities: rqaas, IgnoreResourceRequirements: irr})
	if err != nil {
		log.Error(err)
		return
	}
	for _, rqaa := range resp.Abilities {
		for _, aa := range rqaa.Abilities {
			if u := b.Units.ByTag[rqaa.UnitTag]; u == nil {
				log.Warning("Wat?")
			} else {
				if irr {
					u.IrrAbilities = append(u.IrrAbilities, aa.AbilityId)
				} else {
					u.Abilities = append(u.Abilities, aa.AbilityId)
				}
			}
		}
	}
}

func (b *Bot) SaveState() {
	log.Info("Saving state")
	if _, err := os.Stat("data"); os.IsNotExist(err) {
		if err := os.Mkdir("data", 755); err != nil {
			log.Fatal(err)
		}
		if _, err := os.Stat("data/state"); os.IsNotExist(err) {
			if err := os.Mkdir("data/state", 755); err != nil {
				log.Fatal(err)
			}
		}
	}

	obs, _ := b.Obs.Marshal()
	data, _ := b.Data.Marshal()
	info, _ := b.Info.Marshal()
	for file, bytes := range map[string][]byte{
		"observation": obs,
		"data":        data,
		"info":        info,
	} {
		fileName := "data/state/" + file + ".bin"
		f, err := os.Create(fileName)
		if err != nil {
			log.Fatal(err)
		}
		f.Write(bytes)
		f.Close()
	}
}

/*func (b *Bot) LoadState() {
	info := &tests.AgentInfo{}
	info.LoadObservation("data/state/observation.bin")
	info.LoadData("data/state/data.bin")
	info.LoadInfo("data/state/info.bin")
	b.Info = info
}*/
