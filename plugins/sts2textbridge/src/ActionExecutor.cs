using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;
using Godot;
using MegaCrit.Sts2.Core.Logging;
using MegaCrit.Sts2.Core.Nodes.GodotExtensions;
using MegaCrit.Sts2.Core.Entities.Cards;
using MegaCrit.Sts2.Core.Models;
using MegaCrit.Sts2.Core.Entities.Creatures;
using MegaCrit.Sts2.Core.Nodes.CommonUi;
using MegaCrit.Sts2.Core.Nodes.Cards.Holders;
using MegaCrit.Sts2.Core.Nodes.Combat;
using MegaCrit.Sts2.Core.Nodes.Screens.CharacterSelect;
using MegaCrit.Sts2.Core.Nodes.Screens.Map;

namespace Sts2TextBridge;

/// <summary>
/// 读 action.json、校验、执行。任何异常都吞掉并写 last_error，绝不让游戏崩。
/// </summary>
public static class ActionExecutor
{
	public static string? LastError { get; private set; }

	private static int _parseFailures;

	public static void ClearError()
	{
		LastError = null;
	}

	/// <summary>返回 true 表示这个文件已经被处理完（可以删）。</summary>
	public static bool ProcessFile(string path)
	{
		if (!File.Exists(path))
		{
			return false;
		}

		// 先原子改名再处理：处理期间新写入的动作不会被随后的删除误删。
		string proc = path + ".processing";
		try
		{
			File.Move(path, proc, overwrite: true);
		}
		catch (Exception e)
		{
			Log.Warn("[bridge] could not take action file: " + e.Message);
			return false;
		}

		string text;
		try
		{
			text = File.ReadAllText(proc);
		}
		catch (Exception e)
		{
			Log.Warn("[bridge] could not read action file: " + e.Message);
			Bridge.TryDelete(proc);
			return false;
		}

		if (string.IsNullOrWhiteSpace(text))
		{
			// 可能是刚创建还没写完，给几次机会
			Bridge.TryDelete(proc);
			return BumpParseFailure("action file is empty");
		}

		JsonDocument doc;
		try
		{
			doc = JsonDocument.Parse(text);
		}
		catch (Exception e)
		{
			Bridge.TryDelete(proc);
			return BumpParseFailure("action file is not valid json: " + e.Message);
		}

		using (doc)
		{
			_parseFailures = 0;
			try
			{
				Execute(doc.RootElement);
			}
			catch (Exception e)
			{
				LastError = "exception while executing: " + e.Message;
				Log.Error("[bridge] action failed: " + e);
			}
		}
		Bridge.TryDelete(proc);
		return true;
	}

	private static bool BumpParseFailure(string message)
	{
		_parseFailures++;
		if (_parseFailures >= 3)
		{
			_parseFailures = 0;
			LastError = message;
			Log.Warn("[bridge] dropping unreadable action file: " + message);
			return true;
		}
		return false;
	}

	private static void Execute(JsonElement root)
	{
		if (root.ValueKind != JsonValueKind.Object)
		{
			Fail("action must be a json object");
			return;
		}
		if (!root.TryGetProperty("action", out JsonElement idEl) || idEl.ValueKind != JsonValueKind.String)
		{
			Fail("missing \"action\" field");
			return;
		}
		string action = idEl.GetString() ?? "";
		Log.Info("[bridge] action: " + action);

		if (Bridge.Quiet)
		{
			// 静默模式下没有常态采集，动作需要自己先看一眼界面
			StateCollector.Collect();
		}

		switch (action)
		{
			case "choose_card":
			{
				int cardIdx = RequireInt(root, "index");
				if (cardIdx < 0 || cardIdx >= RunCollector.RewardHolders.Count)
				{
					Fail($"card {cardIdx} is not on screen (0..{RunCollector.RewardHolders.Count - 1})");
					break;
				}
				NCardHolder? holder = RunCollector.RewardHolders[cardIdx];
				if (!GodotObject.IsInstanceValid(holder))
				{
					Fail($"card {cardIdx} node is freed");
					break;
				}
				ClearError();
				Log.Info($"[bridge] choose_card {cardIdx}");
				holder!.Call("EmitPressed");
				break;
			}
			case "skip_card_reward":
			{
				NClickableControl? alt = RunCollector.SkipButton;
				if (alt == null || !GodotObject.IsInstanceValid(alt))
				{
					Fail("no skip button on screen");
					break;
				}
				ClearError();
				Log.Info("[bridge] skip_card_reward");
				alt!.ForceClick();
				break;
			}
			case "click":
				ClickByIndex(RequireInt(root, "index"));
				break;
			case "menu_click":
				ClickByIndex(RequireInt(root, "index"));
				break;
			case "pick_character":
				int pick = RequireInt(root, "index");
				if (pick < 0)
				{
					break;
				}
				if (pick >= StateCollector.CharButtons.Count)
				{
					Fail($"character {pick} is not on screen");
					break;
				}
				NCharacterSelectButton charButton = StateCollector.CharButtons[pick];
				if (!GodotObject.IsInstanceValid(charButton))
				{
					Fail($"character {pick} node is freed");
					break;
				}
				if (charButton.IsLocked)
				{
					Fail($"character {pick} is locked");
					break;
				}
				ClearError();
				Log.Info($"[bridge] select character {pick}");
				charButton.Select();
				break;
			case "start_run":
			{
				int startIdx = StateCollector.FindStartButtonIndex(StateCollector.CollectClickablesOfCurrentScene());
				if (startIdx < 0)
				{
					Fail("start button is not available yet");
					break;
				}
				ClickByIndex(startIdx);
				break;
			}
			case "map_move":
				MoveOnMap(RequireInt(root, "index"));
				break;
			case "play_card":
			{
				int target = -1;
				if (root.TryGetProperty("target", out JsonElement tEl) && tEl.ValueKind == JsonValueKind.Number && tEl.TryGetInt32(out int tv))
				{
					target = tv;
				}
				PlayCard(RequireInt(root, "index"), target);
				break;
			}
			case "end_turn":
				EndTurn();
				break;
			case "take_reward":
				ClickByIndex(RequireInt(root, "index"));
				break;
			case "proceed":
				Proceed();
				break;
			case "modal_yes":
				PressModal(yes: true);
				break;
			case "modal_no":
				PressModal(yes: false);
				break;
			case "dump_tree":
			{
				bool whole = root.TryGetProperty("whole", out JsonElement wEl) && wEl.ValueKind == JsonValueKind.True;
				string dump = StateCollector.DumpTree(whole);
				string path = Path.Combine(Bridge.Dir, "dump.txt");
				File.WriteAllText(path, dump);
				ClearError();
				Log.Info("[bridge] tree dumped -> " + path + " (" + dump.Length + " chars)");
				break;
			}
			case "wait":
				ClearError();
				break;
			default:
				Fail($"unknown action \"{action}\" in scene");
				break;
		}
	}

	/// <summary>奖励/结算界面上的「继续」。</summary>
	private static void Proceed()
	{
		Node? btn = StateCollector.FindProceed();
		if (btn == null)
		{
			Fail("no proceed button on screen");
			return;
		}
		ClearError();
		Log.Info("[bridge] proceed -> " + StateCollector.PathOf(btn));
		StateCollector.PressNode(btn);
	}

	private static void MoveOnMap(int index)
	{
		if (index < 0 || index >= RunCollector.TravelablePoints.Count)
		{
			Fail($"map point {index} is not travelable (0..{RunCollector.TravelablePoints.Count - 1})");
			return;
		}
		NMapPoint point = RunCollector.TravelablePoints[index];
		if (!GodotObject.IsInstanceValid(point))
		{
			Fail($"map point {index} node is freed");
			return;
		}
		ClearError();
		Log.Info($"[bridge] map_move {index} -> {point.Point.PointType} ({point.Point.coord.col},{point.Point.coord.row})");
		point.ForceClick();
	}

	private static void PlayCard(int index, int targetIndex)
	{
		if (index < 0 || index >= RunCollector.HandCards.Count)
		{
			Fail($"card {index} is not in hand (0..{RunCollector.HandCards.Count - 1})");
			return;
		}
		CardModel card = RunCollector.HandCards[index];
		Creature? target = null;
		if (targetIndex >= 0)
		{
			if (targetIndex >= RunCollector.Enemies.Count)
			{
				Fail($"enemy {targetIndex} is not on screen (0..{RunCollector.Enemies.Count - 1})");
				return;
			}
			target = RunCollector.Enemies[targetIndex];
		}
		bool needsTarget = card.TargetType == TargetType.AnyEnemy || card.TargetType == TargetType.AnyAlly;
		if (needsTarget && target == null)
		{
			if (RunCollector.Enemies.Count == 1)
			{
				target = RunCollector.Enemies[0];
			}
			else
			{
				Fail("this card needs a target; give \"target\" (enemy index)");
				return;
			}
		}
		ClearError();
		Log.Info($"[bridge] play_card {index} ({card.Id.Entry}) target={(target == null ? "-" : target.Name)}");
		if (!card.TryManualPlay(target))
		{
			Fail($"card {card.Id.Entry} cannot be played right now");
		}
	}

	private static void EndTurn()
	{
		NEndTurnButton? button = RunCollector.EndTurnButton();
		if (button == null)
		{
			Fail("no end turn button");
			return;
		}
		if (!button.IsEnabled)
		{
			Fail("end turn button is disabled");
			return;
		}
		ClearError();
		Log.Info("[bridge] end_turn");
		button.ForceClick();
	}

	private static void PressModal(bool yes)
	{
		Node? modal = StateCollector.GetOpenModal();
		if (modal == null)
		{
			Fail("no modal is open");
			return;
		}
		NVerticalPopup? popup = StateCollector.GetPopup(modal);
		NClickableControl? button = popup == null ? null : (yes ? popup.YesButton : popup.NoButton);
		if (button == null || !GodotObject.IsInstanceValid(button))
		{
			Fail("this modal has no " + (yes ? "yes" : "no") + " button");
			return;
		}
		if (!button.IsVisibleInTree() || !button.IsEnabled)
		{
			Fail("modal " + (yes ? "yes" : "no") + " button is not available");
			return;
		}
		ClearError();
		Log.Info($"[bridge] modal {(yes ? "yes" : "no")} ({button.Name})");
		button.ForceClick();
	}

	private static void ClickByIndex(int index)
	{
		if (index < 0)
		{
			Fail("index must be >= 0");
			return;
		}
		List<NClickableControl> clicks = StateCollector.CollectClickablesOfCurrentScene();
		if (index >= clicks.Count)
		{
			Fail($"index {index} out of range (0..{clicks.Count - 1})");
			return;
		}
		NClickableControl target = clicks[index];
		if (!GodotObject.IsInstanceValid(target))
		{
			Fail($"index {index} points at a freed node");
			return;
		}
		if (!target.IsEnabled)
		{
			Fail($"index {index} is disabled");
			return;
		}
		ClearError();
		Log.Info($"[bridge] click {index} ({target.GetType().Name} {target.Name})");
		target.ForceClick();
	}

	private static int RequireInt(JsonElement root, string key, string? fallback = null)
	{
		if (root.TryGetProperty(key, out JsonElement el) && el.ValueKind == JsonValueKind.Number && el.TryGetInt32(out int v))
		{
			return v;
		}
		if (fallback != null && root.TryGetProperty(fallback, out JsonElement el2) && el2.ValueKind == JsonValueKind.Number && el2.TryGetInt32(out int v2))
		{
			return v2;
		}
		Fail($"missing or invalid \"{key}\"");
		return -1;
	}

	private static void Fail(string message)
	{
		LastError = message;
		Log.Warn("[bridge] rejected action: " + message);
	}
}
