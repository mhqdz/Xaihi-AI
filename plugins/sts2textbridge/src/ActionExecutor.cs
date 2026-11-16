using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;
using Godot;
using MegaCrit.Sts2.Core.Logging;
using MegaCrit.Sts2.Core.Nodes.GodotExtensions;
using MegaCrit.Sts2.Core.Nodes.Screens.CharacterSelect;

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

		string text;
		try
		{
			text = File.ReadAllText(path);
		}
		catch (Exception e)
		{
			Log.Warn("[bridge] could not read action file: " + e.Message);
			return false;
		}

		if (string.IsNullOrWhiteSpace(text))
		{
			// 可能是刚创建还没写完，给几次机会
			return BumpParseFailure("action file is empty");
		}

		JsonDocument doc;
		try
		{
			doc = JsonDocument.Parse(text);
		}
		catch (Exception e)
		{
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

		switch (action)
		{
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
			case "wait":
				ClearError();
				break;
			default:
				Fail($"unknown action \"{action}\" in scene");
				break;
		}
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
