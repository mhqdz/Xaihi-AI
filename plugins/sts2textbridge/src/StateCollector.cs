using System;
using System.Collections.Generic;
using System.Linq;
using Godot;
using MegaCrit.Sts2.Core.Logging;
using MegaCrit.Sts2.Core.Nodes;
using MegaCrit.Sts2.Core.Nodes.GodotExtensions;
using MegaCrit.Sts2.Core.Nodes.Screens.CharacterSelect;
using MegaCrit.Sts2.Core.Nodes.Screens.MainMenu;
using MegaCrit.Sts2.Core.Nodes;
using MegaCrit.Sts2.Core.Nodes.CommonUi;
using MegaCrit.Sts2.Core.Nodes.Screens.Overlays;
using MegaCrit.Sts2.Core.Nodes.Rooms;

namespace Sts2TextBridge;

/// <summary>
/// 把当前界面读成文本。原则：能语义化就语义化，拿不准就靠 clickables 兜底。
/// </summary>
public static class StateCollector
{
	/// <summary>对象还在不在（null 和已释放都算不在）。切场景时到处是半死的节点。</summary>
	public static bool Alive(GodotObject? o)
	{
		if (o == null)
		{
			return false;
		}
		try
		{
			return GodotObject.IsInstanceValid(o);
		}
		catch (Exception)
		{
			return false;
		}
	}

	public static BridgeState Collect()
	{
		BridgeState state = new BridgeState();
		Dictionary<string, object?> probe = new Dictionary<string, object?>();

		Bridge.Mark("c.enter");
		NGame? game = NGame.Instance;
		if (!Alive(game))
		{
			state.Scene = "idle";
			probe["reason"] = "NGame.Instance is null or freed";
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

		Bridge.Mark("c.scene=" + scene.GetType().Name);
		probe["root_scene"] = scene.GetType().Name;

		Node? active = null;
		try
		{
			Bridge.Mark("c.modal");
			Node? modal = GetOpenModal();
			if (modal != null)
			{
				probe["modal"] = modal.GetType().Name;
				active = modal;
				state.Scene = "modal";
			}
			else if (RunCollector.IsInRun())
			{
				// root_scene 切换有延迟：NRun 已经在了就以 NRun 为准
				Bridge.Mark("c.inrun.tryfill");
				if (RunCollector.TryFill(state, probe))
				{
					Bridge.Mark("c.inrun.filled");
					active = RunCollector.GetActiveRoot() ?? scene;
				}
				else
				{
					state.Scene = "loading";
					probe["note"] = "NRun is up but no room node is ready yet";
				}
			}
			else if (scene is NMainMenu menu)
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
				// 既不在局内、也不是菜单：场景切换中
				state.Scene = "loading";
				probe["note"] = "scene is transitioning";
			}
		}
		catch (Exception e)
		{
			state.Scene = "error";
			probe["error"] = e.ToString();
			state.Probe = probe;
			return state;
		}

		LastActiveRoot = active;
		Bridge.Mark("c.clicks");
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
				if (clicks.Count == 0)
				{
					// 刚点完「出发」：预载资源、建 RunState、切场景，这期间屏幕上没有可点项。
					state.Scene = "loading";
					probe["note"] = "char select is transitioning (embark in flight)";
					break;
				}
				FillCharSelect(state, clicks);
				break;
			case "modal":
				FillModal(state, active, clicks);
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

		Bridge.Mark("c.done scene=" + state.Scene);
		state.LastError = ActionExecutor.LastError;
		state.Probe = probe;
		return state;
	}

	/// <summary>当前应当被操作的那个节点（子菜单优先于主菜单）。</summary>
	public static Node? GetActiveRoot()
	{
		Node? modal = GetOpenModal();
		if (modal != null)
		{
			return modal;
		}
		if (Alive(NRun.Instance))
		{
			Node? inRun = RunCollector.GetActiveRoot();
			if (inRun != null)
			{
				return inRun;
			}
		}
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

	/// <summary>当前打开的模态弹窗（教程提示、确认框……）。没有就返回 null。</summary>
	public static Node? GetOpenModal()
	{
		NModalContainer? container = NModalContainer.Instance;
		if (!Alive(container))
		{
			return null;
		}
		if (container == null)
		{
			return null;
		}
		// OpenModal 才是真正的弹窗；Backstop 只是它背后那层灰幕。
		if (container.OpenModal is Node open && GodotObject.IsInstanceValid(open) && open.IsInsideTree())
		{
			return open;
		}
		Node? found = null;
		foreach (Node child in container.GetChildren())
		{
			if (child.Name.ToString().Contains("Backstop"))
			{
				continue;
			}
			if (child is CanvasItem ci && !ci.IsVisibleInTree())
			{
				continue;
			}
			found = child;
		}
		return found;
	}

	/// <summary>弹窗里的 NVerticalPopup（标题 + 正文 + 是/否）。</summary>
	public static NVerticalPopup? GetPopup(Node? modal)
	{
		if (modal == null || !GodotObject.IsInstanceValid(modal))
		{
			return null;
		}
		if (modal is NVerticalPopup direct)
		{
			return direct;
		}
		return FindFirst<NVerticalPopup>(modal, 4);
	}

	private static T? FindFirst<T>(Node node, int depth) where T : class
	{
		if (depth < 0)
		{
			return null;
		}
		foreach (Node child in node.GetChildren())
		{
			if (child is T hit)
			{
				return hit;
			}
			T? deeper = FindFirst<T>(child, depth - 1);
			if (deeper != null)
			{
				return deeper;
			}
		}
		return null;
	}

	private static void FillModal(BridgeState state, Node? modal, List<NClickableControl> clicks)
	{
		string title = "";
		string body = "";
		NVerticalPopup? popup = GetPopup(modal);
		if (popup != null)
		{
			title = RawText(popup.GetNodeOrNull("Header"));
			body = RawText(popup.GetNodeOrNull("Description"));
		}
		string joined = (title + (title.Length > 0 && body.Length > 0 ? " — " : "") + body).Trim();
		state.Prompt = joined.Length > 0 ? joined : (modal != null ? LabelOf(modal) : "");

		state.Options = new List<OptionInfo>();
		for (int i = 0; i < clicks.Count; i++)
		{
			if (clicks[i] is not NPopupYesNoButton button)
			{
				continue;
			}
			string name = button.Name.ToString();
			bool isYes = name.StartsWith("Yes");
			string text = RawText(button);
			state.Options.Add(new OptionInfo
			{
				Index = state.Options.Count,
				Text = (isYes ? "是: " : "否: ") + (text.Length > 0 ? text : name),
				Disabled = !button.IsEnabled,
			});
			state.AvailableActions.Add(ActionSpec.Make(isYes ? "modal_yes" : "modal_no"));
		}
	}

	/// <summary>只取文字，不退回节点名——弹窗里要区分「有没有文字」。</summary>
	private static string RawText(Node? node)
	{
		if (node == null)
		{
			return "";
		}
		List<string> parts = new List<string>();
		if (node is Label l)
		{
			parts.Add(l.Text);
		}
		else if (node is RichTextLabel r)
		{
			parts.Add(r.Text);
		}
		GatherText(node, parts, 0);
		return string.Join(" ", parts.Where(p => !string.IsNullOrWhiteSpace(p)).Select(p => p.Trim())).Trim();
	}

	public static List<NClickableControl> CollectClickablesOfCurrentScene()
	{
		return CollectClickables(GetActiveRoot());
	}

	/// <summary>上一次采集到的可点元素，执行动作时按 index 取。</summary>
	public static List<NClickableControl> LastClickables { get; private set; } = new List<NClickableControl>();

	/// <summary>上一次采集时的活动根节点（调试用）。</summary>
	public static Node? LastActiveRoot { get; private set; }

	/// <summary>把活动根节点（或整棵场景树）连类型和可见性一起打印出来，排障用。</summary>
	public static string DumpTree(bool wholeTree)
	{
		Node? root = wholeTree ? (Node?)NGame.Instance?.GetTree()?.Root : LastActiveRoot;
		if (root == null)
		{
			root = NGame.Instance?.RootSceneContainer?.CurrentScene;
		}
		if (root == null)
		{
			return "<no root>";
		}
		System.Text.StringBuilder sb = new System.Text.StringBuilder();
		sb.Append("root=").Append(root.GetType().FullName).Append(" name=").Append(root.Name).Append('\n');
		DumpNode(root, sb, 0, 10);
		return sb.ToString();
	}

	private static void DumpNode(Node node, System.Text.StringBuilder sb, int depth, int maxDepth)
	{
		foreach (Node child in node.GetChildren())
		{
			string vis = child is CanvasItem ci ? (ci.IsVisibleInTree() ? "vis" : "HID") : "---";
			string extra = "";
			if (child is NClickableControl cc)
			{
				extra = " [clickable" + (cc.IsEnabled ? "" : ",disabled") + "]";
			}
			if (child is Control c)
			{
				extra += $" rect={c.Position.X:0},{c.Position.Y:0} {c.Size.X:0}x{c.Size.Y:0}";
			}
			sb.Append(new string(' ', depth * 2)).Append(child.GetType().Name).Append(" '").Append(child.Name).Append("' ").Append(vis).Append(extra).Append('\n');
			if (depth + 1 < maxDepth && child.GetChildCount() > 0)
			{
				DumpNode(child, sb, depth + 1, maxDepth);
			}
		}
	}

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
		// 场景切换时节点会被 free。先拷一份 children，再逐个验活——
		// 直接 foreach GetChildren() 碰上正在释放的节点会让引擎原生崩掉。
		if (!GodotObject.IsInstanceValid(node) || !node.IsInsideTree())
		{
			return;
		}
		Node[] kids;
		try
		{
			kids = node.GetChildren().ToArray();
		}
		catch (Exception)
		{
			return;
		}
		foreach (Node child in kids)
		{
			if (!GodotObject.IsInstanceValid(child) || !child.IsInsideTree())
			{
				continue;
			}
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
		if (!Alive(node))
		{
			return "";
		}
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
	// ── 「继续」类按钮：不靠 NClickableControl，按名字在树里找 ─────────────

	private static readonly string[] ProceedNeedles = { "proceed", "continue", "skip" };

	/// <summary>依次在覆盖层、当前场景、整棵树里找「继续」按钮。</summary>
	public static Node? FindProceed()
	{
		List<Node?> roots = new List<Node?>();
		if (Alive(NOverlayStack.Instance))
		{
			roots.Add(NOverlayStack.Instance!.Peek() as Node);
		}
		roots.Add(NGame.Instance?.RootSceneContainer?.CurrentScene);
		roots.Add(NGame.Instance?.GetTree()?.Root);

		foreach (Node? root in roots)
		{
			if (root == null || !GodotObject.IsInstanceValid(root))
			{
				continue;
			}
			List<Node> hits = new List<Node>();
			FindAllByName(root, 0, 14, hits);
			if (hits.Count == 0)
			{
				continue;
			}
			Node? pick = null;
			foreach (Node h in hits)
			{
				if (h is BaseButton bb && bb.IsVisibleInTree() && !bb.Disabled)
				{
					pick = h;
					break;
				}
			}
			if (pick == null)
			{
				foreach (Node h in hits)
				{
					if (h is CanvasItem ci && ci.IsVisibleInTree())
					{
						pick = h;
						break;
					}
				}
			}
			pick ??= hits[0];
			Log.Info("[bridge] proceed candidate: " + PathOf(pick) + " (" + hits.Count + " hits under " + root.GetType().Name + ")");
			return pick;
		}
		return null;
	}

	private static void FindAllByName(Node node, int depth, int maxDepth, List<Node> acc)
	{
		if (depth > maxDepth)
		{
			return;
		}
		foreach (Node child in node.GetChildren())
		{
			if (!GodotObject.IsInstanceValid(child))
			{
				continue;
			}
			string id = (child.GetType().Name + " " + child.Name).ToLowerInvariant();
			foreach (string needle in ProceedNeedles)
			{
				if (id.Contains(needle))
				{
					acc.Add(child);
					break;
				}
			}
			FindAllByName(child, depth + 1, maxDepth, acc);
		}
	}

	public static string PathOf(Node node)
	{
		List<string> parts = new List<string>();
		Node? cur = node;
		for (int i = 0; i < 12 && cur != null; i++)
		{
			parts.Insert(0, cur.GetType().Name + "'" + cur.Name + "'");
			cur = cur.GetParent();
		}
		return string.Join("/", parts);
	}

	/// <summary>按下去：先试语义方法，再试信号，最后塞一次真实鼠标点击。</summary>
	public static void PressNode(Node node)
	{
		if (node is NClickableControl cc && cc.IsEnabled)
		{
			cc.ForceClick();
			return;
		}
		if (node is BaseButton btn)
		{
			if (btn.Disabled)
			{
				Log.Warn("[bridge] proceed button is disabled");
				return;
			}
			btn.EmitSignal(BaseButton.SignalName.Pressed);
			return;
		}
		foreach (string name in new[] { "ForceClick", "Click", "Press", "OnPressed", "Activate" })
		{
			var mi = node.GetType().GetMethod(name, System.Reflection.BindingFlags.Public | System.Reflection.BindingFlags.Instance, Type.EmptyTypes);
			if (mi != null)
			{
				mi.Invoke(node, null);
				return;
			}
		}
		if (node is Control ctrl)
		{
			Vector2 p = ctrl.GetGlobalRect().GetCenter();
			Viewport? vp = ctrl.GetViewport();
			vp?.PushInput(new InputEventMouseMotion { Position = p, GlobalPosition = p });
			vp?.PushInput(new InputEventMouseButton { Position = p, GlobalPosition = p, ButtonIndex = MouseButton.Left, Pressed = true });
			vp?.PushInput(new InputEventMouseButton { Position = p, GlobalPosition = p, ButtonIndex = MouseButton.Left, Pressed = false });
			return;
		}
		Log.Warn("[bridge] proceed node is not pressable: " + node.GetType().Name);
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
