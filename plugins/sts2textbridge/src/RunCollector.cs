using System;
using System.Collections.Generic;
using System.Linq;
using System.Reflection;
using System.Threading.Tasks;
using Godot;
using MegaCrit.Sts2.Core.Combat;
using MegaCrit.Sts2.Core.Logging;
using MegaCrit.Sts2.Core.Entities.Cards;
using MegaCrit.Sts2.Core.Entities.Merchant;
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
using MegaCrit.Sts2.Core.Nodes.Screens.Shops;
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
		if (roomName == "shop")
		{
			// 商店的商品名/价格/类型读得出来，就别拿 LabelOf 去糊整棵树的文字
			// （那会把商人身上每个 Label、连字体图集的字形编号一起拼进 prompt）。
			FillShop(state, probe);
		}
		else
		{
			state.Prompt = roomNode != null ? StateCollector.LabelOf(roomNode) : "";
		}
		Bridge.Mark("tf.r.done");
		return true;
	}

	// ── 商店 ────────────────────────────────────────────────────────────────

	/// <summary>
	/// 还在卖的槽位，按屏幕上的顺序（卡牌 → 遗物 → 药水 → 删牌服务）。
	/// 买和读都走这一份顺序，agent 看到第 i 项就 buy i。
	/// </summary>
	public static List<NMerchantSlot> ShopSlots()
	{
		List<NMerchantSlot> result = new List<NMerchantSlot>();
		NRun? run = NRun.Instance;
		if (!StateCollector.Alive(run) || !StateCollector.Alive(run!.MerchantRoom))
		{
			return result;
		}
		foreach (NMerchantSlot slot in FindAll<NMerchantSlot>(run.MerchantRoom))
		{
			if (!StateCollector.Alive(slot) || !slot.IsInsideTree() || !slot.IsVisibleInTree())
			{
				continue;
			}
			MerchantEntry? entry = null;
			try
			{
				entry = slot.Entry;
				if (entry == null || !entry.IsStocked)
				{
					continue;
				}
			}
			catch (Exception)
			{
				continue;
			}
			result.Add(slot);
		}
		return result;
	}

	private static void FillShop(BridgeState state, Dictionary<string, object?> probe)
	{
		state.Items = new List<ItemInfo>();
		state.Prompt = "商店：buy <index> 买，leave 走人";
		List<NMerchantSlot> slots = ShopSlots();
		probe["shop_slots"] = slots.Count;
		for (int i = 0; i < slots.Count; i++)
		{
			NMerchantSlot slot = slots[i];
			ItemInfo info = new ItemInfo
			{
				Index = i,
				Id = slot.Name.ToString(),
				Kind = "item",
				Enabled = true,
			};
			MerchantEntry? entry = null;
			try
			{
				entry = slot.Entry;
				if (entry != null)
				{
					info.Price = entry.Cost;
					info.Enabled = entry.EnoughGold;
				}
			}
			catch (Exception)
			{
			}
			try
			{
				switch (entry)
				{
					case MerchantCardEntry ce:
						info.Kind = "card";
						CardModel? card = ce.CreationResult?.Card;
						if (card != null)
						{
							info.Name = card.Title;
							info.Desc = CardSummary(card);
						}
						break;
					case MerchantRelicEntry re:
						info.Kind = "relic";
						info.Name = re.Model != null ? re.Model.Title.GetFormattedText() : null;
						if (string.IsNullOrWhiteSpace(info.Name))
						{
							info.Name = re.Model?.Id.Entry;
						}
						break;
					case MerchantPotionEntry pe:
						info.Kind = "potion";
						info.Name = pe.Model != null ? pe.Model.Title.GetFormattedText() : null;
						if (string.IsNullOrWhiteSpace(info.Name))
						{
							info.Name = pe.Model?.Id.Entry;
						}
						break;
					case MerchantCardRemovalEntry:
						info.Kind = "removal";
						info.Name = "删掉牌组里的一张牌";
						break;
				}
			}
			catch (Exception e)
			{
				probe["shop_item_" + i] = e.GetType().Name + ": " + e.Message;
			}
			if (string.IsNullOrWhiteSpace(info.Name))
			{
				info.Name = slot.Name.ToString();
			}
			state.Items.Add(info);
			state.AvailableActions.Add(ActionSpec.Make("buy", ("index", i)));
		}
		state.AvailableActions.Add(ActionSpec.Make("leave"));
	}

	/// <summary>一张牌的「类型 · 稀有度 —— 说明」，给 agent 判断值不值这个价。</summary>
	private static string CardSummary(CardModel card)
	{
		string head = card.Rarity + " " + card.Type;
		string desc = "";
		try
		{
			desc = card.GetDescriptionForPile(PileType.None) ?? "";
		}
		catch (Exception)
		{
		}
		// 能量图标在文本里就是 [img]…energy_icon.png[/img]，读屏时换成「能量」，
		// 连续两个再合并成「2能量」，不然一句话里蹦出「能量能量」。
		desc = System.Text.RegularExpressions.Regex.Replace(desc, @"\[img\][^\[]*\[/img\]", "\u0001");
		desc = System.Text.RegularExpressions.Regex.Replace(desc, @"(\u0001)+",
			m => (m.Value.Length == 1 ? "" : m.Value.Length.ToString()) + "能量");
		desc = System.Text.RegularExpressions.Regex.Replace(desc, @"\[/?[a-z]+\]", "");
		desc = desc.Replace("\n", " ").Trim();
		if (desc.Length > 160)
		{
			desc = desc.Substring(0, 160);
		}
		return string.IsNullOrWhiteSpace(desc) ? head : head + " —— " + desc;
	}

	/// <summary>
	/// 买第 index 项。走 MerchantInventory.TryPurchaseEntryWithIdUnsynchronized / 
	/// MerchantEntry.OnTryPurchaseUnsynchronized —— 界面点下去也是这条链，库存、金币、视觉一起更新。
	/// （0.99.1 反编译源码里的 OnTryPurchaseWrapper 在实机 DLL 里已改名，别照抄。）
	/// </summary>
	public static void BuyItem(int index)
	{
		List<NMerchantSlot> slots = ShopSlots();
		if (index < 0 || index >= slots.Count)
		{
			ActionExecutor.Fail("shop item " + index + " is not on sale (have " + slots.Count + ")");
			return;
		}
		MerchantEntry? entry = null;
		try
		{
			entry = slots[index].Entry;
		}
		catch (Exception)
		{
		}
		if (entry == null)
		{
			ActionExecutor.Fail("shop item " + index + " has no entry");
			return;
		}
		MerchantInventory? inventory = null;
		try
		{
			inventory = NMerchantRoom.Instance?.Inventory?.Inventory;
		}
		catch (Exception)
		{
		}
		ActionExecutor.ClearError();
		Log.Info("[bridge] buy " + index + " (" + entry.GetType().Name + " cost=" + entry.Cost + " id=" + entry.Id + ")");
		BuyAsync(entry, inventory);
	}

	private static async void BuyAsync(MerchantEntry entry, MerchantInventory? inventory)
	{
		try
		{
			if (inventory == null)
			{
				ActionExecutor.Fail("no merchant inventory on screen");
				return;
			}
			// 药水槽满了的话，OnTryPurchaseUnsynchronized 只会安静地返回 false
			if (entry is MerchantPotionEntry && entry.Player != null && !entry.Player.HasOpenPotionSlots)
			{
				ActionExecutor.Fail("potion slots are full (" + entry.Player.PotionSlots.Count + "/" + entry.Player.MaxPotionCount + "), drink or drop one first");
				return;
			}
			bool ok = await entry.OnTryPurchaseUnsynchronized(inventory, false);
			Log.Info("[bridge] buy: OnTryPurchaseUnsynchronized -> " + ok);
			if (ok)
			{
				return;
			}
			bool locked = false;
			try
			{
				locked = inventory.TryBeginPurchase();
				if (locked)
				{
					ok = await entry.OnTryPurchaseUnsynchronized(inventory, false);
				}
			}
			finally
			{
				if (locked)
				{
					inventory.EndPurchase();
				}
			}
			Log.Info("[bridge] buy: with lock(" + locked + ") -> " + ok);
			if (!ok)
			{
				ActionExecutor.Fail("purchase refused (cost=" + entry.Cost + ", gold=" + (entry.Player != null ? entry.Player.Gold : -1) + ", stocked=" + entry.IsStocked + ")");
			}
		}
		catch (Exception e)
		{
			ActionExecutor.Fail("buy failed: " + e.GetType().Name + ": " + e.Message);
			Log.Warn("[bridge] buy failed: " + e);
		}
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

		// 敌人一律以数据层 cs.Enemies 为准，下标就是 cs.Enemies 的下标。
		// 不按屏幕上的节点数：群怪（第 13 层那批）会叠在一起，视觉数量少于数据层。
		// 死掉的也保留（带 alive=false），否则后面下标会和 cs.Enemies 错位。
		state.Enemies = new List<EnemyInfo>();
		for (int i = 0; i < cs.Enemies.Count; i++)
		{
			Creature enemy = cs.Enemies[i];
			Enemies.Add(enemy);
			if (enemy == null)
			{
				state.Enemies.Add(new EnemyInfo { Index = i, Name = "<null>", Alive = false });
				continue;
			}
			state.Enemies.Add(new EnemyInfo
			{
				Index = i,
				Id = enemy.ModelId.Entry,
				Name = enemy.Name,
				Hp = enemy.CurrentHp,
				MaxHp = enemy.MaxHp,
				Block = enemy.Block,
				Alive = enemy.IsAlive,
				Intent = DescribeIntent(enemy),
			});
		}
		int aliveCount = 0;
		foreach (Creature e in Enemies) if (e != null && e.IsAlive) aliveCount++;
		probe["enemies"] = Enemies.Count;
		probe["enemies_alive"] = aliveCount;

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
				int onlyAlive = OnlyAliveIndex();
				if (needsTarget && onlyAlive >= 0)
				{
					state.AvailableActions.Add(ActionSpec.Make("play_card", ("index", idx), ("target", onlyAlive)));
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
	}

	/// <summary>场上只剩一个活敌时返回它在 cs.Enemies 里的下标，否则 -1。</summary>
	public static int OnlyAliveIndex()
	{
		int found = -1;
		for (int i = 0; i < Enemies.Count; i++)
		{
			if (Enemies[i] == null || !Enemies[i].IsAlive) continue;
			if (found >= 0) return -1;
			found = i;
		}
		return found;
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
