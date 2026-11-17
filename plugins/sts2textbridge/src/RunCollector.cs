using System;
using System.Collections.Generic;
using System.Linq;
using System.Reflection;
using Godot;
using MegaCrit.Sts2.Core.Combat;
using MegaCrit.Sts2.Core.Entities.Cards;
using MegaCrit.Sts2.Core.Entities.Creatures;
using MegaCrit.Sts2.Core.Entities.Players;
using MegaCrit.Sts2.Core.Map;
using MegaCrit.Sts2.Core.Models;
using MegaCrit.Sts2.Core.Nodes;
using MegaCrit.Sts2.Core.Nodes.Cards.Holders;
using MegaCrit.Sts2.Core.Nodes.Combat;
using MegaCrit.Sts2.Core.Nodes.CommonUi;
using MegaCrit.Sts2.Core.Nodes.GodotExtensions;
using MegaCrit.Sts2.Core.Nodes.Rooms;
using MegaCrit.Sts2.Core.Nodes.Screens;
using MegaCrit.Sts2.Core.Nodes.Screens.CardSelection;
using MegaCrit.Sts2.Core.Nodes.Screens.Map;
using MegaCrit.Sts2.Core.Nodes.Screens.Overlays;
using MegaCrit.Sts2.Core.Runs;

namespace Sts2TextBridge;

/// <summary>
/// 局内（地图 / 战斗 / 奖励）的读屏与操作。
/// </summary>
public static class RunCollector
{
	private static FieldInfo? _stateField;

	/// <summary>卡牌三选一的候选（按 index 对应，供 choose_card 用）。</summary>
	public static readonly List<NCardHolder> RewardHolders = new();

	/// <summary>三选一界面上的「跳过」按钮。</summary>
	public static NClickableControl? SkipButton { get; private set; }

	/// <summary>卡牌三选一：牌在 NCardHolder 里，点的是 holder 的 Pressed 信号，不是那个 Hitbox。</summary>
	private static void FillCardReward(BridgeState state, NCardRewardSelectionScreen screen)
	{
		RewardHolders.Clear();
		SkipButton = null;
		foreach (NClickableControl c in FindAll<NClickableControl>(screen))
		{
			if (StateCollector.Alive(c) && c.GetType().Name.Contains("Alternative"))
			{
				SkipButton = c;
				break;
			}
		}
		state.Cards = new List<SelectCardInfo>();
		state.Prompt = "选一张牌加入牌组";
		state.CanSkip = true;
		foreach (NCardHolder holder in FindAll<NCardHolder>(screen))
		{
			if (!StateCollector.Alive(holder))
			{
				continue;
			}
			CardModel? model = null;
			try
			{
				model = holder.CardModel;
			}
			catch (Exception)
			{
			}
			if (model == null)
			{
				continue;
			}
			int idx = RewardHolders.Count;
			RewardHolders.Add(holder);
			state.Cards.Add(new SelectCardInfo
			{
				Index = idx,
				Id = model.Id.Entry,
				Name = StateCollector.SafeText(model.TitleLocString),
				Upgraded = model.CurrentUpgradeLevel > 0,
			});
			state.AvailableActions.Add(ActionSpec.Make("choose_card", ("index", idx)));
		}
		state.AvailableActions.Add(ActionSpec.Make("skip_card_reward"));
	}

	/// <summary>从 NRun 上拿地图界面（比 NMapScreen.Instance 稳，切换期间那个静态引用会发虚）。</summary>
	public static NMapScreen? MapScreenOf(NRun? run)
	{
		if (!StateCollector.Alive(run))
		{
			return null;
		}
		try
		{
			NGlobalUi? ui = run!.GlobalUi;
			if (!StateCollector.Alive(ui))
			{
				return null;
			}
			return ui!.MapScreen;
		}
		catch (Exception)
		{
			return null;
		}
	}

	/// <summary>地图上可去的点，按下标对应。</summary>
	public static List<NMapPoint> TravelablePoints { get; } = new List<NMapPoint>();

	/// <summary>手牌，按下标对应。</summary>
	public static List<CardModel> HandCards { get; } = new List<CardModel>();

	/// <summary>敌人，按下标对应（play_card 的 target 就是这里的下标）。</summary>
	public static List<Creature> Enemies { get; } = new List<Creature>();

	/// <summary>NRun 已经建起来了吗（进游戏了，不一定界面就绪）。</summary>
	public static bool IsInRun()
	{
		return StateCollector.Alive(NRun.Instance);
	}

	public static RunState? GetRunState()
	{
		NRun? run = NRun.Instance;
		if (!StateCollector.Alive(run))
		{
			return null;
		}
		_stateField ??= typeof(NRun).GetField("_state", BindingFlags.NonPublic | BindingFlags.Instance);
		return _stateField?.GetValue(run) as RunState;
	}

	public static Player? LocalPlayer()
	{
		CombatState? cs = CombatManager.Instance?.DebugOnlyGetState();
		Player? p = cs?.Players.FirstOrDefault();
		if (p != null)
		{
			return p;
		}
		return GetRunState()?.Players.FirstOrDefault();
	}

	/// <summary>局内界面：返回 true 表示已经填好了 state。</summary>
	public static bool TryFill(BridgeState state, Dictionary<string, object?> probe)
	{
		Bridge.Mark("tf.enter");
		NRun? run = NRun.Instance;
		if (!StateCollector.Alive(run))
		{
			return false;
		}

		Bridge.Mark("tf.state");
		RunState? rs = GetRunState();
		Bridge.Mark("tf.state.done");
		if (rs != null)
		{
			state.Floor = rs.TotalFloor;
			probe["floor"] = rs.TotalFloor;
			probe["act_floor"] = rs.ActFloor;
			probe["act"] = rs.CurrentActIndex + 1;
		}

		Bridge.Mark("tf.player");
		FillPlayer(state);
		Bridge.Mark("tf.player.done");

		// 1) 覆盖层：奖励、牌堆、游戏结束……
		Node? overlay = StateCollector.Alive(NOverlayStack.Instance)
			? NOverlayStack.Instance!.Peek() as Node
			: null;
		if (overlay != null)
		{
			probe["overlay"] = overlay.GetType().Name;
			if (overlay is NCardRewardSelectionScreen cardScreen)
			{
				state.Scene = "card_reward";
				FillCardReward(state, cardScreen);
				return true;
			}
			if (overlay is NRewardsScreen)
			{
				state.Scene = "reward";
				FillRewards(state);
				if (!state.AvailableActions.Any(a => (a["id"] as string) == "proceed"))
				{
					state.AvailableActions.Add(ActionSpec.Make("proceed"));
				}
				return true;
			}
			if (overlay.GetType().Name.Contains("GameOver"))
			{
				state.Scene = "game_over";
				state.Prompt = StateCollector.LabelOf(overlay);
				return true;
			}
			state.Scene = "overlay";
			state.Prompt = StateCollector.LabelOf(overlay);
			return true;
		}

		// 2) 战斗
		Bridge.Mark("tf.combat");
		NCombatRoom? combat = run.CombatRoom;
		if (combat != null && CombatManager.Instance.IsInProgress)
		{
			state.Scene = "combat";
			FillCombat(state, probe);
			return true;
		}

		// 3) 地图：RoomContainer 的当前场景是 NMapRoom 就是地图
		Bridge.Mark("tf.map");
		NMapScreen? mapScreen = MapScreenOf(run);
		Bridge.Mark("tf.map.screen=" + (mapScreen != null) + " room=" + (run.MapRoom != null));
		if (run.MapRoom != null || (mapScreen != null && mapScreen.IsOpen))
		{
			state.Scene = "map";
			FillMap(state, probe);
			return true;
		}

		// 4) 其他房间（事件、休息、商店……）先只报个名，靠 clickables 兜底
		Bridge.Mark("tf.room");
		probe["room"] = run.GetType().Name;
		string roomName = "room";
		Bridge.Mark("tf.r.event");
		if (run.EventRoom != null) { roomName = "event"; }
		else if (run.RestSiteRoom != null) { roomName = "rest"; }
		else if (run.MerchantRoom != null) { roomName = "shop"; }
		else if (run.TreasureRoom != null) { roomName = "treasure"; }
		Bridge.Mark("tf.r.=" + roomName);
		state.Scene = roomName;
		Node? roomNode = GetActiveRoomNode();
		Bridge.Mark("tf.r.node=" + (roomNode != null));
		state.Prompt = roomNode != null ? StateCollector.LabelOf(roomNode) : "";
		Bridge.Mark("tf.r.done");
		return true;
	}

	private static Node? GetActiveRoomNode()
	{
		NRun? run = NRun.Instance;
		if (!StateCollector.Alive(run))
		{
			return null;
		}
		return (Node?)run.CombatRoom ?? (Node?)run.EventRoom ?? (Node?)run.RestSiteRoom
			?? (Node?)run.MerchantRoom ?? (Node?)run.TreasureRoom ?? (Node?)run.MapRoom;
	}

	/// <summary>局内时，屏幕上的「当前界面」根节点。</summary>
	public static Node? GetActiveRoot()
	{
		Node? overlay = StateCollector.Alive(NOverlayStack.Instance)
			? NOverlayStack.Instance!.Peek() as Node
			: null;
		if (overlay != null)
		{
			return overlay;
		}
		NRun? run = NRun.Instance;
		if (!StateCollector.Alive(run))
		{
			return null;
		}
		if (run.CombatRoom != null)
		{
			return run.CombatRoom;
		}
		if (run.EventRoom != null) { return run.EventRoom; }
		if (run.RestSiteRoom != null) { return run.RestSiteRoom; }
		if (run.MerchantRoom != null) { return run.MerchantRoom; }
		if (run.TreasureRoom != null) { return run.TreasureRoom; }
		if (run.MapRoom != null)
		{
			return StateCollector.Alive(NMapScreen.Instance) ? (Node?)NMapScreen.Instance : (Node?)run.MapRoom;
		}
		return run;
	}

	private static void FillPlayer(BridgeState state)
	{
		Player? p = LocalPlayer();
		if (p == null)
		{
			return;
		}
		Creature c = p.Creature;
		PlayerInfo info = new PlayerInfo
		{
			Hp = c.CurrentHp,
			MaxHp = c.MaxHp,
			Gold = p.Gold,
			Block = c.Block,
		};
		PlayerCombatState? pcs = p.PlayerCombatState;
		if (pcs != null)
		{
			info.Energy = pcs.Energy;
			info.MaxEnergy = pcs.MaxEnergy;
		}
		if (c.Powers.Count > 0)
		{
			info.Powers = c.Powers.Select(x => SafeName(x)).ToList();
		}
		state.Player = info;

		if (p.Relics.Count > 0)
		{
			state.Relics = p.Relics.Select(r => StateCollector.SafeText(r.Title)).ToList();
		}
		if (p.Potions.Any())
		{
			state.Potions = p.Potions.Select((potion, i) => new PotionInfo
			{
				Index = i,
				Id = potion.Id.Entry,
				Name = StateCollector.SafeText(potion.Title),
				Usable = p.CanUseOrRemovePotions,
			}).ToList();
		}
	}

	private static string SafeName(PowerModel power)
	{
		try
		{
			string text = StateCollector.SafeText(power.Title);
			return string.IsNullOrWhiteSpace(text) ? power.Id.Entry : text;
		}
		catch (Exception)
		{
			return power.Id.Entry;
		}
	}

	private static void FillMap(BridgeState state, Dictionary<string, object?> probe)
	{
		TravelablePoints.Clear();
		state.Nodes = new List<MapNodeInfo>();
		NMapScreen? screen = MapScreenOf(NRun.Instance);
		RunState? rs = GetRunState();
		if (rs != null)
		{
			MapCoord? here = rs.CurrentMapCoord;
			probe["current_coord"] = here.HasValue ? $"{here.Value.col},{here.Value.row}" : "<none>";
		}
		if (screen == null)
		{
			probe["map_screen"] = "<null>";
			return;
		}
		probe["travel_enabled"] = screen.IsTravelEnabled;

		foreach (NMapPoint point in FindAll<NMapPoint>(screen))
		{
			if (point.State != MapPointState.Travelable || !point.IsEnabled)
			{
				continue;
			}
			int idx = TravelablePoints.Count;
			TravelablePoints.Add(point);
			state.Nodes.Add(new MapNodeInfo
			{
				Index = idx,
				Row = point.Point.coord.row,
				Col = point.Point.coord.col,
				Kind = point.Point.PointType.ToString(),
				LeadsTo = null,
			});
			state.AvailableActions.Add(ActionSpec.Make("map_move", ("index", idx)));
		}
		probe["travelable"] = TravelablePoints.Count;
		state.Prompt = TravelablePoints.Count > 0 ? "选一条路" : "地图上暂时没有可走的点";
	}

	private static void FillCombat(BridgeState state, Dictionary<string, object?> probe)
	{
		HandCards.Clear();
		Enemies.Clear();

		CombatState? cs = CombatManager.Instance?.DebugOnlyGetState();
		Player? p = LocalPlayer();
		if (cs == null || p == null)
		{
			probe["combat_state"] = "<null>";
			return;
		}

		foreach (Creature enemy in cs.Enemies)
		{
			if (!enemy.IsAlive)
			{
				continue;
			}
			int idx = Enemies.Count;
			Enemies.Add(enemy);
			state.Enemies ??= new List<EnemyInfo>();
			state.Enemies.Add(new EnemyInfo
			{
				Index = idx,
				Id = enemy.ModelId.Entry,
				Name = enemy.Name,
				Hp = enemy.CurrentHp,
				MaxHp = enemy.MaxHp,
				Block = enemy.Block,
				Intent = DescribeIntent(enemy),
			});
		}

		PlayerCombatState? pcs = p.PlayerCombatState;
		if (pcs == null)
		{
			probe["player_combat_state"] = "<null>";
			return;
		}

		probe["phase"] = pcs.Phase.ToString();
		probe["turn"] = pcs.TurnNumber;

		state.Hand = new List<CardInfo>();
		foreach (CardModel card in pcs.Hand.Cards)
		{
			int idx = HandCards.Count;
			HandCards.Add(card);
			bool playable = false;
			try
			{
				playable = card.CanPlay();
			}
			catch (Exception)
			{
			}
			state.Hand.Add(new CardInfo
			{
				Index = idx,
				Id = card.Id.Entry,
				Name = StateCollector.SafeText(card.TitleLocString),
				Cost = card.EnergyCost.GetResolved(),
				Playable = playable,
				NeedsTarget = card.TargetType == TargetType.AnyEnemy || card.TargetType == TargetType.AnyAlly,
			});
			// 只列真能打出去的牌：列了打不出去的，agent 会反复试同一张
			if (playable)
			{
				bool needsTarget = card.TargetType == TargetType.AnyEnemy || card.TargetType == TargetType.AnyAlly;
				if (needsTarget && Enemies.Count == 1)
				{
					state.AvailableActions.Add(ActionSpec.Make("play_card", ("index", idx), ("target", 0)));
				}
				else
				{
					state.AvailableActions.Add(ActionSpec.Make("play_card", ("index", idx)));
				}
			}
		}

		// 手牌正被「拿着」（点了牌在等目标）时，别的按钮全是灰的：
		// 这时只给 cancel，否则 agent 会以为游戏卡死了。
		if (IsHandBusy())
		{
			state.AvailableActions.Clear();
			state.AvailableActions.Add(ActionSpec.Make("cancel"));
			probe["hand_busy"] = true;
		}
		else if (CanEndTurn())
		{
			state.AvailableActions.Add(ActionSpec.Make("end_turn"));
		}
		probe["hand"] = HandCards.Count;
		probe["enemies"] = Enemies.Count;
	}

	private static string? DescribeIntent(Creature enemy)
	{
		MonsterModel? monster = enemy.Monster;
		if (monster == null)
		{
			return null;
		}
		try
		{
			List<string> parts = new List<string>();
			foreach (var intent in monster.NextMove.Intents)
			{
				string name = intent.GetType().Name.Replace("Intent", "");
				parts.Add(name);
			}
			if (parts.Count == 0)
			{
				return monster.IntendsToAttack ? "Attack" : null;
			}
			return string.Join("+", parts.Distinct());
		}
		catch (Exception)
		{
			return null;
		}
	}

	/// <summary>手牌是不是正处在「选中/拖拽一张牌，等目标」的状态。</summary>
	public static bool IsHandBusy()
	{
		NPlayerHand? hand = PlayerHand();
		if (hand == null || !GodotObject.IsInstanceValid(hand))
		{
			return false;
		}
		try
		{
			return hand.InCardPlay || hand.CurrentMode != NPlayerHand.Mode.Play;
		}
		catch (Exception)
		{
			return false;
		}
	}

	public static NPlayerHand? PlayerHand()
	{
		NPlayerHand? hand = StateCollector.Alive(NRun.Instance) ? NRun.Instance!.CombatRoom?.Ui?.Hand : null;
		return hand != null && GodotObject.IsInstanceValid(hand) ? hand : null;
	}

	public static bool CanEndTurn()
	{
		NEndTurnButton? button = StateCollector.Alive(NRun.Instance) ? NRun.Instance!.CombatRoom?.Ui?.EndTurnButton : null;
		return button != null && GodotObject.IsInstanceValid(button) && button.IsEnabled;
	}

	public static NEndTurnButton? EndTurnButton()
	{
		NEndTurnButton? button = StateCollector.Alive(NRun.Instance) ? NRun.Instance!.CombatRoom?.Ui?.EndTurnButton : null;
		if (button == null || !GodotObject.IsInstanceValid(button))
		{
			return null;
		}
		return button;
	}

	private static void FillRewards(BridgeState state)
	{
		state.Rewards = new List<RewardInfo>();
		List<NClickableControl> clicks = StateCollector.CollectClickablesOfCurrentScene();
		for (int i = 0; i < clicks.Count; i++)
		{
			state.Rewards.Add(new RewardInfo
			{
				Index = i,
				Kind = clicks[i].GetType().Name,
				Name = StateCollector.LabelOf(clicks[i]),
			});
			state.AvailableActions.Add(ActionSpec.Make("take_reward", ("index", i)));
		}
		state.Prompt = "选一项奖励，或继续";
	}

	private static IEnumerable<T> FindAll<T>(Node node) where T : Node
	{
		foreach (Node child in node.GetChildren())
		{
			if (child is T hit)
			{
				yield return hit;
			}
			if (child.GetChildCount() > 0)
			{
				foreach (T deeper in FindAll<T>(child))
				{
					yield return deeper;
				}
			}
		}
	}
}
