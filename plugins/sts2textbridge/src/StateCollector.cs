using System;
using System.Collections.Generic;
using System.Linq;
using Godot;
using MegaCrit.Sts2.Core.Logging;
using MegaCrit.Sts2.Core.Nodes;
using MegaCrit.Sts2.Core.Nodes.GodotExtensions;
using MegaCrit.Sts2.Core.Nodes.Screens.CharacterSelect;
using MegaCrit.Sts2.Core.Nodes.Screens.MainMenu;
using MegaCrit.Sts2.Core.Nodes.Screens.Overlays;

namespace Sts2TextBridge;

/// <summary>
/// 把当前界面读成文本。原则：能语义化就语义化，拿不准就靠 clickables 兜底。
/// </summary>
public static class StateCollector
{
	public static BridgeState Collect()
	{
		BridgeState state = new BridgeState();
		Dictionary<string, object?> probe = new Dictionary<string, object?>();

		NGame? game = NGame.Instance;
		if (game == null)
		{
			state.Scene = "idle";
			probe["reason"] = "NGame.Instance is null";
			state.Probe = probe;
			return state;
		}

		Control? scene = game.RootSceneContainer?.CurrentScene;
		if (scene == null)
		{
			state.Scene = "idle";
			probe["root_scene"] = "<null>";
			state.Probe = probe;
			return state;
		}

		probe["root_scene"] = scene.GetType().Name;

		Node? active = null;
		try
		{
			if (scene is NMainMenu menu)
			{
				NSubmenu? sub = menu.SubmenuStack?.Peek();
				probe["submenu"] = sub?.GetType().Name ?? "<none>";
				active = (Node?)sub ?? menu;
				state.Scene = sub switch
				{
					null => "menu",
					NCharacterSelectScreen => "char_select",
					_ => "menu",
				};
			}
			else
			{
				// 非主菜单（游戏内）——本版先不处理，留给下一步
				active = scene;
				state.Scene = "idle";
				probe["note"] = "in-run scene not handled yet";
			}
		}
		catch (Exception e)
		{
			state.Scene = "error";
			probe["error"] = e.ToString();
			state.Probe = probe;
			return state;
		}

		List<NClickableControl> clicks = CollectClickables(active);
		probe["clickable_count"] = clicks.Count;
		LastClickables = clicks;

		if (clicks.Count > 0)
		{
			state.Clickables = new List<ClickableInfo>(clicks.Count);
			for (int i = 0; i < clicks.Count; i++)
			{
				state.Clickables.Add(new ClickableInfo
				{
					Index = i,
					Label = LabelOf(clicks[i]),
					Kind = clicks[i].GetType().Name,
				});
			}
		}

		switch (state.Scene)
		{
			case "menu":
				FillMenu(state, clicks);
				break;
			case "char_select":
				FillCharSelect(state, clicks);
				break;
		}

		// 兜底：任何可点的东西都能通过 click 点到
		for (int i = 0; i < clicks.Count; i++)
		{
			state.AvailableActions.Add(ActionSpec.Make("click", ("index", i)));
		}
		if (state.AvailableActions.Count == 0)
		{
			state.AvailableActions.Add(ActionSpec.Make("wait"));
		}

		state.LastError = ActionExecutor.LastError;
		state.Probe = probe;
		return state;
	}

	/// <summary>当前应当被操作的那个节点（子菜单优先于主菜单）。</summary>
	public static Node? GetActiveRoot()
	{
		Control? scene = NGame.Instance?.RootSceneContainer?.CurrentScene;
		if (scene is NMainMenu menu)
		{
			return (Node?)menu.SubmenuStack?.Peek() ?? menu;
		}
		return scene;
	}

	/// <summary>角色下标 -> clickables 下标，避免让调用方自己拼。</summary>
	public static Dictionary<int, int> CharToClick { get; } = new Dictionary<int, int>();

	/// <summary>角色下标 -> 按钮本体。</summary>
	public static List<NCharacterSelectButton> CharButtons { get; } = new List<NCharacterSelectButton>();

	public static List<NClickableControl> CollectClickablesOfCurrentScene()
	{
		return CollectClickables(GetActiveRoot());
	}

	/// <summary>上一次采集到的可点元素，执行动作时按 index 取。</summary>
	public static List<NClickableControl> LastClickables { get; private set; } = new List<NClickableControl>();

	public static List<NClickableControl> CollectClickables(Node? root)
	{
		List<NClickableControl> result = new List<NClickableControl>();
		if (root != null)
		{
			Walk(root, result);
		}
		return result;
	}

	private static void Walk(Node node, List<NClickableControl> outList)
	{
		foreach (Node child in node.GetChildren())
		{
			if (child is CanvasItem ci && !ci.IsVisibleInTree())
			{
				continue;
			}
			if (child is NClickableControl cc && cc.IsEnabled)
			{
				outList.Add(cc);
			}
			if (child.GetChildCount() > 0)
			{
				Walk(child, outList);
			}
		}
	}

	/// <summary>按钮上能读到的文字：所有后代 Label/RichTextLabel 的文本拼起来。</summary>
	public static string LabelOf(Node node)
	{
		List<string> parts = new List<string>();
		GatherText(node, parts, 0);
		string joined = string.Join(" ", parts.Where(p => !string.IsNullOrWhiteSpace(p)).Select(p => p.Trim()));
		if (string.IsNullOrWhiteSpace(joined))
		{
			joined = node.Name.ToString();
		}
		return joined.Length > 120 ? joined.Substring(0, 120) : joined;
	}

	private static void GatherText(Node node, List<string> parts, int depth)
	{
		if (depth > 6)
		{
			return;
		}
		foreach (Node child in node.GetChildren())
		{
			switch (child)
			{
				case Label label:
					parts.Add(label.Text);
					break;
				case RichTextLabel rich:
					parts.Add(rich.Text);
					break;
			}
			if (child.GetChildCount() > 0)
			{
				GatherText(child, parts, depth + 1);
			}
		}
	}

	private static void FillMenu(BridgeState state, List<NClickableControl> clicks)
	{
		state.Items = new List<ItemInfo>();
		for (int i = 0; i < clicks.Count; i++)
		{
			state.Items.Add(new ItemInfo
			{
				Index = i,
				Id = clicks[i].Name.ToString(),
				Label = LabelOf(clicks[i]),
				Enabled = clicks[i].IsEnabled,
			});
			state.AvailableActions.Add(ActionSpec.Make("menu_click", ("index", i)));
		}
	}

	private static void FillCharSelect(BridgeState state, List<NClickableControl> clicks)
	{
		state.Characters = new List<CharacterInfo>();
		CharToClick.Clear();
		CharButtons.Clear();
		for (int i = 0; i < clicks.Count; i++)
		{
			if (clicks[i] is not NCharacterSelectButton button)
			{
				continue;
			}
			int idx = state.Characters.Count;
			string id = "random";
			string name = "随机";
			if (!button.IsRandom && button.Character != null)
			{
				id = button.Character.Id.Entry;
				name = SafeText(button.Character.Title);
			}
			state.Characters.Add(new CharacterInfo
			{
				Index = idx,
				Id = id,
				Name = name,
				Locked = button.IsLocked,
			});
			state.AvailableActions.Add(ActionSpec.Make("pick_character", ("index", idx)));
			CharToClick[idx] = i;
			CharButtons.Add(button);
		}

		if (FindStartButtonIndex(clicks) >= 0)
		{
			state.AvailableActions.Add(ActionSpec.Make("start_run"));
		}
	}

	/// <summary>在 clickables 里找「出发」按钮的下标，找不到返回 -1。</summary>
	public static int FindStartButtonIndex(List<NClickableControl> clicks)
	{
		for (int i = 0; i < clicks.Count; i++)
		{
			if (clicks[i].Name.ToString() == "ConfirmButton")
			{
				return i;
			}
		}
		return -1;
	}

	/// <summary>LocString 取值，取不到就退回 Id。</summary>
	public static string SafeText(MegaCrit.Sts2.Core.Localization.LocString? loc)
	{
		try
		{
			string? text = loc?.GetFormattedText();
			if (!string.IsNullOrWhiteSpace(text))
			{
				return text!;
			}
		}
		catch (Exception)
		{
			// 本地化缺失不算错误
		}
		return "";
	}
}

public static class ActionSpec
{
	public static Dictionary<string, object?> Make(string id, params (string Key, object? Value)[] extra)
	{
		Dictionary<string, object?> dict = new Dictionary<string, object?> { ["id"] = id };
		foreach ((string Key, object? Value) in extra)
		{
			dict[Key] = Value;
		}
		return dict;
	}
}
