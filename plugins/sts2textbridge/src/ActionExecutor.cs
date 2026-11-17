using System;
using System.Collections.Generic;
using System.IO;
using System.Reflection;
using System.Text;
using System.Text.Json;
using System.Threading.Tasks;
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
using MegaCrit.Sts2.Core.Commands;
using MegaCrit.Sts2.Core.Combat;
using MegaCrit.Sts2.Core.GameActions.Multiplayer;
using MegaCrit.Sts2.Core.Helpers;

namespace Sts2TextBridge;

/// <summary>
/// 读 action.json、校验、执行。任何异常都吞掉并写 last_error，绝不让游戏崩。
/// </summary>
public static class ActionExecutor
{
	public static string? LastError { get; private set; }

	/// <summary>有异步动作（目前只有出牌）还没落地。此期间不吃新动作，免得两次出牌搅在一起。</summary>
	public static bool Busy { get; private set; }

	private static int _parseFailures;

	public static void ClearError()
	{
		LastError = null;
	}

	/// <summary>返回 true 表示这个文件已经被处理完（可以删）。</summary>
	public static bool ProcessFile(string path)
	{
		if (Busy)
		{
			return false;
		}
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
			case "cancel":
			{
				// 手牌被「拿着」时唯一的出路：把那张牌放回手里。
				NPlayerHand? hand = RunCollector.PlayerHand();
				if (hand == null)
				{
					Fail("no hand on screen");
					break;
				}
				ClearError();
				Log.Info("[bridge] cancel card play");
				hand.CancelAllCardPlay();
				break;
			}
			case "take_reward":
				ClickByIndex(RequireInt(root, "index"));
				break;
			case "proceed":
			case "leave":
				Proceed();
				break;
			case "buy":
				RunCollector.BuyItem(RequireInt(root, "index"));
				break;
			case "modal_yes":
				PressModal(yes: true);
				break;
			case "modal_no":
				PressModal(yes: false);
				break;
			case "diag":
				Diag();
				break;
			case "diag_enemies":
				DiagEnemies();
				break;
			case "diag_click":
				DiagClick();
				break;
			case "diag_proceed":
				DiagProceed();
				break;
			case "diag_alpha":
				DiagAlpha();
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

	// ── 探测：栈顶覆盖层的 alpha / 位置，判断它到底是「看得见」还是「已淡出」──────
	private static void DiagAlpha()
	{
		StringBuilder sb = new StringBuilder();
		object? stack = CallStaticByName("NOverlayStack", "Instance");
		sb.AppendLine("ScreenCount = " + (Call(stack, "ScreenCount")?.ToString() ?? "?"));
		object? peek = Call(stack, "Peek");
		sb.AppendLine("peek = " + (peek?.GetType().FullName ?? "null"));
		if (peek is CanvasItem ci)
		{
			sb.AppendLine("Visible          = " + ci.Visible);
			sb.AppendLine("IsVisibleInTree  = " + ci.IsVisibleInTree());
			sb.AppendLine("Modulate         = " + ci.Modulate + "   A=" + ci.Modulate.A);
			sb.AppendLine("SelfModulate     = " + ci.SelfModulate + "   A=" + ci.SelfModulate.A);
			sb.AppendLine("Color(a)         = " + ((peek as Control)?.GetThemeColor("font_color").ToString() ?? "-"));
			sb.AppendLine("Position         = " + ((peek as Control)?.Position.ToString() ?? "-"));
			sb.AppendLine("GlobalPosition   = " + ((peek as Control)?.GlobalPosition.ToString() ?? "-"));
			sb.AppendLine("Size             = " + ((peek as Control)?.Size.ToString() ?? "-"));
			if (peek is Control c2) sb.AppendLine("GlobalRect       = " + c2.GetGlobalRect() + "  VisibleRect=" + c2.GetGlobalRect().Intersection(new Rect2(0, 0, 1920, 1080)));
			sb.AppendLine("ZIndex           = " + ci.ZIndex);
			sb.AppendLine("ProcessMode      = " + ((Node)peek).ProcessMode);
			// 父链上有没有人 alpha 是 0（Modulate 会沿树相乘）
			Node? cur = peek as Node;
			int guard = 0;
			while (cur != null && guard++ < 8)
			{
				if (cur is CanvasItem pci)
					sb.AppendLine("  chain " + cur.Name + " modulateA=" + pci.Modulate.A + " selfA=" + pci.SelfModulate.A + " visible=" + pci.Visible + " inTree=" + pci.IsVisibleInTree());
				cur = cur.GetParent();
			}
			// 同一张屏上的按钮能不能点（mouseFilter / 位置）
			Node? btn = StateCollector.FindProceed();
			sb.AppendLine("proceed btn = " + (btn == null ? "null" : StateCollector.PathOf(btn)));
			if (btn is Control bc)
				sb.AppendLine("  btn rect=" + bc.GetGlobalRect() + " filter=" + bc.MouseFilter + " inTree=" + bc.IsVisibleInTree() + " modulateA=" + bc.Modulate.A + " selfA=" + bc.SelfModulate.A);
		}
		// 地图侧
		List<string> maps = new List<string>();
		Node? root = (CallStaticByName("NGame", "Instance") as Node)?.GetTree()?.Root;
		if (root != null) FindByTypeName(root, "MapScreen", 0, 14, maps);
		sb.AppendLine("MapScreenNodes = [" + string.Join(", ", maps) + "]");
		// 光标底下是谁
		Vector2 m = ((Node?)peek)?.GetViewport()?.GetMousePosition() ?? Vector2.Zero;
		sb.AppendLine("mouse = " + m);
		Node? hit = ((Node?)peek)?.GetViewport()?.GuiGetHoveredControl();
		sb.AppendLine("hovered = " + (hit == null ? "null" : StateCollector.PathOf(hit)));
		File.WriteAllText(Path.Combine(Bridge.Dir, "diag.txt"), sb.ToString());
		ClearError();
		Log.Info("[bridge] diag_alpha done");
	}

	// ── 探测：直接调 OnProceedButtonPressed 并把内部异常挖出来，再试 NOverlayStack 的关法 ──
	private static void DiagProceed()
	{
		StringBuilder sb = new StringBuilder();
		Node? node = StateCollector.FindProceed();
		sb.AppendLine("BEFORE " + Snapshot());
		if (node == null) { sb.AppendLine("no proceed node"); File.WriteAllText(Path.Combine(Bridge.Dir, "diag.txt"), sb.ToString()); return; }
		sb.AppendLine("node = " + StateCollector.PathOf(node));
		Node? screen = node.GetParent();
		System.Reflection.MethodInfo? mi = screen?.GetType().GetMethod("OnProceedButtonPressed",
			System.Reflection.BindingFlags.Instance | System.Reflection.BindingFlags.Public | System.Reflection.BindingFlags.NonPublic,
			null, new[] { typeof(NButton) }, null);
		sb.AppendLine("method = " + (mi == null ? "null" : mi.ToString()));
		object? stack = CallStaticByName("NOverlayStack", "Instance");
		if (mi != null && screen != null)
		{
			try
			{
				mi.Invoke(screen, new object[] { node });
				sb.AppendLine("invoke ok");
			}
			catch (System.Reflection.TargetInvocationException e)
			{
				sb.AppendLine("INVOKE THREW: " + e.InnerException);
			}
			catch (Exception e)
			{
				sb.AppendLine("INVOKE THREW(outer): " + e);
			}
		}
		sb.AppendLine("AFTER-INVOKE " + Snapshot());
		// 覆盖层栈有哪些关法
		if (stack != null)
		{
			List<string> ms = new List<string>();
			foreach (System.Reflection.MethodInfo m in stack.GetType().GetMethods(System.Reflection.BindingFlags.Public | System.Reflection.BindingFlags.Instance))
			{
				if (m.IsSpecialName) continue;
				ms.Add(m.Name + "(" + m.GetParameters().Length + ")");
			}
			sb.AppendLine("stack methods = " + string.Join(", ", ms));
			object? peek = Call(stack, "Peek");
			if (peek != null)
			{
				foreach (string name in new[] { "Remove", "Pop", "Hide", "Close" })
				{
					System.Reflection.MethodInfo? m = stack.GetType().GetMethod(name,
						System.Reflection.BindingFlags.Public | System.Reflection.BindingFlags.Instance, null, new[] { peek.GetType() }, null)
						?? stack.GetType().GetMethod(name, System.Reflection.BindingFlags.Public | System.Reflection.BindingFlags.Instance, null, Type.EmptyTypes, null);
					if (m == null) { sb.AppendLine("  " + name + ": not found"); continue; }
					try
					{
						m.Invoke(stack, m.GetParameters().Length == 0 ? null : new[] { peek });
						sb.AppendLine("  " + name + ": ok (" + m.GetParameters().Length + " args)");
					}
					catch (System.Reflection.TargetInvocationException e) { sb.AppendLine("  " + name + " THREW: " + e.InnerException); }
					catch (Exception e) { sb.AppendLine("  " + name + " THREW: " + e.Message); }
					sb.AppendLine("  after " + name + ": " + Snapshot());
				}
			}
		}
		File.WriteAllText(Path.Combine(Bridge.Dir, "diag.txt"), sb.ToString());
		SceneTree? tree = node.GetTree();
		if (tree != null)
		{
			SceneTreeTimer t = tree.CreateTimer(1.5);
			t.Timeout += () => { try { File.WriteAllText(Path.Combine(Bridge.Dir, "diag_after.txt"), "AFTER(1.5s) " + Snapshot()); } catch { } };
		}
	}

	// ── 探测：对「继续」按钮做一次真实模拟点击，点前点后各抓一次快照 ──────────
	private static void DiagClick()
	{
		StringBuilder sb = new StringBuilder();
		Node? node = StateCollector.FindProceed();
		if (node == null)
		{
			sb.AppendLine("no proceed node found");
			File.WriteAllText(Path.Combine(Bridge.Dir, "diag.txt"), sb.ToString());
			return;
		}
		Control? ctrl = node as Control;
		Vector2 p = ctrl != null ? ctrl.GetGlobalRect().GetCenter() : Vector2.Zero;
		sb.AppendLine("node   = " + StateCollector.PathOf(node));
		sb.AppendLine("type   = " + node.GetType().Name + "  (BaseButton=" + (node is BaseButton) + ", Disabled=" + ((node as BaseButton)?.Disabled.ToString() ?? "-") + ")");
		sb.AppendLine("center = " + p);
		if (ctrl != null)
		{
			sb.AppendLine("rect   = " + ctrl.GetGlobalRect() + "  mouseFilter=" + ctrl.MouseFilter + "  visibleInTree=" + ctrl.IsVisibleInTree());
		}
		sb.AppendLine("BEFORE " + Snapshot());
		Viewport? vp = node.GetViewport();
		sb.AppendLine("viewport = " + (vp?.GetType().Name ?? "null"));
		Input.WarpMouse(p);
		Input.ParseInputEvent(new InputEventMouseMotion { Position = p, GlobalPosition = p });
		Input.ParseInputEvent(new InputEventMouseButton { ButtonIndex = MouseButton.Left, Pressed = true, Position = p, GlobalPosition = p });
		Input.ParseInputEvent(new InputEventMouseButton { ButtonIndex = MouseButton.Left, Pressed = false, Position = p, GlobalPosition = p });
		File.WriteAllText(Path.Combine(Bridge.Dir, "diag.txt"), sb.ToString());
		Log.Info("[bridge] diag_click at " + p);
		SceneTree? tree = node.GetTree();
		if (tree != null)
		{
			SceneTreeTimer t = tree.CreateTimer(1.0);
			t.Timeout += () =>
			{
				try
				{
					File.WriteAllText(Path.Combine(Bridge.Dir, "diag_after.txt"), "AFTER(1.0s) " + Snapshot());
				}
				catch (Exception e) { Log.Warn("[bridge] diag_after failed: " + e.Message); }
			};
		}
	}

	private static string Snapshot()
	{
		StringBuilder sb = new StringBuilder();
		object? stack = CallStaticByName("NOverlayStack", "Instance");
		sb.Append("ScreenCount=" + (Call(stack, "ScreenCount")?.ToString() ?? "?"));
		object? peek = Call(stack, "Peek");
		sb.Append("  peek=" + (peek?.GetType().Name ?? "null"));
		if (peek is CanvasItem ci)
		{
			sb.Append("(Visible=" + ci.Visible + ",InTree=" + ci.IsVisibleInTree()
				+ ",modA=" + ci.Modulate.A.ToString("0.###") + ",selfA=" + ci.SelfModulate.A.ToString("0.###") + ")");
		}
		// 地图：不靠属性名，直接在场景树里按类型名找
		List<string> maps = new List<string>();
		Node? scene = (CallStaticByName("NGame", "Instance") as Node);
		Node? root = scene?.GetTree()?.Root;
		if (root != null) FindByTypeName(root, "MapScreen", 0, 14, maps);
		sb.Append("  MapScreenNodes=[" + string.Join(", ", maps) + "]");
		// GlobalUi 上名字带 Map 的成员（找真名）
		object? run = CallStatic("MegaCrit.Sts2.Core.Runs.RunManager", "Instance");
		object? gui = Call(run, "GlobalUi");
		sb.Append("  GlobalUi=" + (gui?.GetType().Name ?? "null"));
		if (gui != null)
		{
			List<string> names = new List<string>();
			foreach (System.Reflection.PropertyInfo pi in gui.GetType().GetProperties(System.Reflection.BindingFlags.Public | System.Reflection.BindingFlags.Instance))
			{
				if (pi.Name.ToLowerInvariant().Contains("map")) names.Add(pi.Name);
			}
			foreach (System.Reflection.FieldInfo fi in gui.GetType().GetFields(System.Reflection.BindingFlags.NonPublic | System.Reflection.BindingFlags.Public | System.Reflection.BindingFlags.Instance))
			{
				if (fi.Name.ToLowerInvariant().Contains("map")) names.Add("_" + fi.Name);
			}
			sb.Append("  MapMembers=[" + string.Join(", ", names) + "]");
		}
		return sb.ToString();
	}

	private static void FindByTypeName(Node node, string needle, int depth, int maxDepth, List<string> acc)
	{
		if (depth > maxDepth || !GodotObject.IsInstanceValid(node)) return;
		foreach (Node c in node.GetChildren())
		{
			if (!GodotObject.IsInstanceValid(c)) continue;
			if (c.GetType().Name.Contains(needle))
			{
				acc.Add(c.GetType().Name + "'" + c.Name + "'" + (c is CanvasItem ci ? "(Visible=" + ci.Visible + ",InTree=" + ci.IsVisibleInTree() + ")" : ""));
			}
			FindByTypeName(c, needle, depth + 1, maxDepth, acc);
		}
	}

	// ── 一次性诊断：覆盖层栈 + 地图界面（开发期用，别进运行路径）──────────
	private static void Diag()
	{
		StringBuilder sb = new StringBuilder();
		try
		{
			object? stack = CallStaticByName("NOverlayStack", "Instance");
			sb.AppendLine("== OverlayStack ==");
			DumpObj(sb, "stack", stack, 0);
			object? peek = Call(stack, "Peek");
			sb.AppendLine("peek type = " + (peek?.GetType().FullName ?? "null"));
			if (peek is Node pn)
			{
				sb.AppendLine("peek name = " + pn.Name);
				sb.AppendLine("peek Visible = " + (pn as CanvasItem)?.Visible);
				sb.AppendLine("peek IsVisibleInTree = " + ((pn as CanvasItem)?.IsVisibleInTree()));
				sb.AppendLine("peek path = " + StateCollector.PathOf(pn));
			}
			sb.AppendLine();
			sb.AppendLine("== MapScreen ==");
			object? run = CallStatic("MegaCrit.Sts2.Core.Runs.RunManager", "Instance");
			object? globalUi = Call(run, "GlobalUi");
			object? map = Call(globalUi, "MapScreen");
			sb.AppendLine("map type = " + (map?.GetType().FullName ?? "null"));
			DumpObj(sb, "map", map, 0);
			if (map is Node mn)
			{
				sb.AppendLine("map Visible = " + (mn as CanvasItem)?.Visible + " IsVisibleInTree = " + ((mn as CanvasItem)?.IsVisibleInTree()));
				List<Node> pts = new List<Node>();
				Collect(mn, 0, 12, pts);
				sb.AppendLine("map points found = " + pts.Count);
				foreach (Node n in pts)
				{
					sb.AppendLine("-- " + StateCollector.PathOf(n));
					DumpObj(sb, "  pt", n, 0);
				}
			}
		}
		catch (Exception e)
		{
			sb.AppendLine("!! " + e);
		}
		string path = Path.Combine(Bridge.Dir, "diag.txt");
		File.WriteAllText(path, sb.ToString());
		ClearError();
		Log.Info("[bridge] diag -> " + path + " (" + sb.Length + " chars)");
	}

	// ── 临时探测：数据层里到底有没有敌人（只打印，不改采集）──────────────
	private static void DiagEnemies()
	{
		StringBuilder sb = new StringBuilder();
		const BindingFlags F = BindingFlags.Public | BindingFlags.NonPublic | BindingFlags.Instance;
		try
		{
			object? cm = CombatManager.Instance;
			sb.AppendLine("CombatManager.Instance = " + (cm == null ? "<null>" : cm.GetType().FullName!));
			if (cm != null)
			{
				Type t = cm.GetType();
				sb.AppendLine("-- props with \"State\" --");
				foreach (PropertyInfo pi in t.GetProperties(F))
				{
					if (!pi.Name.Contains("State")) continue;
					string v;
					try { v = pi.GetValue(cm)?.ToString() ?? "<null>"; } catch (Exception e) { v = "!! " + e.GetType().Name + ": " + e.Message; }
					sb.AppendLine("  prop " + pi.Name + " : " + pi.PropertyType.Name + " = " + v);
				}
				sb.AppendLine("-- fields with \"State\" --");
				foreach (FieldInfo fi in t.GetFields(F))
				{
					if (!fi.Name.Contains("State")) continue;
					string v;
					try { v = fi.GetValue(cm)?.ToString() ?? "<null>"; } catch (Exception e) { v = "!! " + e.GetType().Name; }
					sb.AppendLine("  field " + fi.Name + " : " + fi.FieldType.Name + " = " + v);
				}
				sb.AppendLine("-- methods with \"State\" (0 params) --");
				foreach (MethodInfo mi in t.GetMethods(F))
				{
					if (!mi.Name.Contains("State") || mi.GetParameters().Length != 0) continue;
					sb.AppendLine("  method " + mi.Name + "() -> " + mi.ReturnType.Name);
				}
			}

			CombatState? cs = CombatManager.Instance?.DebugOnlyGetState();
			sb.AppendLine("cs = " + (cs == null ? "<null>" : cs.GetType().FullName!));
			if (cs != null)
			{
				sb.AppendLine("cs.Enemies = " + (cs.Enemies == null ? "<null>" : cs.Enemies.Count.ToString()));
				if (cs.Enemies != null)
				{
					int i = 0;
					foreach (Creature e in cs.Enemies)
					{
						if (e == null) { sb.AppendLine("  [" + i + "] <null>"); i++; continue; }
						sb.AppendLine("  [" + i + "] Name=" + e.Name + " Hp=" + e.CurrentHp + "/" + e.MaxHp + " alive=" + e.IsAlive);
						i++;
					}
				}
			}
		}
		catch (Exception e)
		{
			sb.AppendLine("!! " + e);
		}
		string path = Path.Combine(Bridge.Dir, "diag_enemies.txt");
		File.WriteAllText(path, sb.ToString());
		ClearError();
		Log.Info("[bridge] diag_enemies -> " + path);
	}

	private static void Collect(Node node, int depth, int maxDepth, List<Node> acc)
	{
		if (depth > maxDepth || !GodotObject.IsInstanceValid(node)) return;
		foreach (Node c in node.GetChildren())
		{
			if (!GodotObject.IsInstanceValid(c)) continue;
			if (c.GetType().Name.Contains("MapPoint")) acc.Add(c);
			Collect(c, depth + 1, maxDepth, acc);
		}
	}

	private static void DumpObj(StringBuilder sb, string label, object? o, int depth)
	{
		if (o == null) { sb.AppendLine(label + " = null"); return; }
		Type t = o.GetType();
		sb.AppendLine(label + " :: " + t.FullName);
		foreach (System.Reflection.PropertyInfo pi in t.GetProperties(System.Reflection.BindingFlags.Public | System.Reflection.BindingFlags.Instance))
		{
			if (pi.GetIndexParameters().Length > 0) continue;
			string v;
			try { object? r = pi.GetValue(o); v = r == null ? "null" : (r is GodotObject go && !GodotObject.IsInstanceValid(go)) ? "<freed>" : r.ToString() ?? "null"; }
			catch (Exception e) { v = "<" + e.GetType().Name + ">"; }
			sb.AppendLine("  " + pi.Name + " = " + v);
		}
	}

	private static object? Call(object? o, string name)
	{
		if (o == null) return null;
		System.Reflection.PropertyInfo? pi = o.GetType().GetProperty(name, System.Reflection.BindingFlags.Public | System.Reflection.BindingFlags.Instance);
		if (pi != null) { try { return pi.GetValue(o); } catch { return null; } }
		System.Reflection.MethodInfo? mi = o.GetType().GetMethod(name, System.Reflection.BindingFlags.Public | System.Reflection.BindingFlags.Instance, null, Type.EmptyTypes, null);
		if (mi != null) { try { return mi.Invoke(o, null); } catch { return null; } }
		return null;
	}

	private static object? CallStatic(string typeName, string name)
	{
		Type? t = null;
		foreach (System.Reflection.Assembly a in AppDomain.CurrentDomain.GetAssemblies())
		{
			t = a.GetType(typeName);
			if (t != null) break;
		}
		if (t == null) return null;
		System.Reflection.PropertyInfo? pi = t.GetProperty(name, System.Reflection.BindingFlags.Public | System.Reflection.BindingFlags.Static);
		if (pi != null) { try { return pi.GetValue(null); } catch { return null; } }
		return null;
	}

	private static object? CallStaticByName(string simpleTypeName, string member)
	{
		Type? t = null;
		foreach (System.Reflection.Assembly a in AppDomain.CurrentDomain.GetAssemblies())
		{
			try
			{
				foreach (Type x in a.GetTypes())
				{
					if (x.Name == simpleTypeName) { t = x; break; }
				}
			}
			catch { }
			if (t != null) break;
		}
		if (t == null) return null;
		System.Reflection.PropertyInfo? pi = t.GetProperty(member, System.Reflection.BindingFlags.Public | System.Reflection.BindingFlags.Static);
		if (pi != null) { try { return pi.GetValue(null); } catch { return null; } }
		return null;
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
		Node? screen = btn.GetParent();
		StateCollector.PressNode(btn);
		// OnProceedButtonPressed 在某些状态下不会把自己从覆盖层栈里摘掉（2026-11-17 实测：
		// 调用成功、无异常、ScreenCount 仍是 1，地图已经可见但帘子还盖着）。
		// 兜底：0.6s 后如果同一张屏还在栈顶，就直接 Remove 掉。
		if (screen != null && btn.GetTree() != null)
		{
			SceneTreeTimer t = btn.GetTree().CreateTimer(0.6);
			t.Timeout += () =>
			{
				try
				{
					if (!GodotObject.IsInstanceValid(screen)) return;
					object? stack = CallStaticByName("NOverlayStack", "Instance");
					object? peek = Call(stack, "Peek");
					if (peek == null || !ReferenceEquals(peek, screen)) return;
					System.Reflection.MethodInfo? rm = stack!.GetType().GetMethod("Remove",
						System.Reflection.BindingFlags.Public | System.Reflection.BindingFlags.Instance, null, new[] { peek.GetType() }, null);
					if (rm == null) { Log.Warn("[bridge] fallback remove: no Remove method"); return; }
					rm.Invoke(stack, new[] { peek });
					Log.Warn("[bridge] proceed fallback: overlay still up, removed " + screen.Name);
				}
				catch (Exception e) { Log.Warn("[bridge] proceed fallback failed: " + e.Message); }
			};
		}
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
				Fail($"enemy {targetIndex} is not in cs.Enemies (0..{RunCollector.Enemies.Count - 1})");
				return;
			}
			target = RunCollector.Enemies[targetIndex];
			if (target == null || !target.IsAlive)
			{
				Fail($"enemy {targetIndex} is already dead; target index is the cs.Enemies index");
				return;
			}
		}
		bool needsTarget = card.TargetType == TargetType.AnyEnemy || card.TargetType == TargetType.AnyAlly;
		if (needsTarget && target == null)
		{
			int onlyAlive = RunCollector.OnlyAliveIndex();
			if (onlyAlive >= 0)
			{
				target = RunCollector.Enemies[onlyAlive];
			}
			else
			{
				Fail("this card needs a target; give \"target\" (enemy index)");
				return;
			}
		}
		if (!card.CanPlay(out UnplayableReason reason, out AbstractModel? _))
		{
			Fail($"card {card.Id.Entry} cannot be played: {reason}");
			return;
		}
		ClearError();
		Log.Info($"[bridge] play_card {index} ({card.Id.Entry}) target={(target == null ? "-" : target.Name)}");
		// 不走 CardModel.TryManualPlay / NCardPlay：那条路要手牌的视觉节点做 holder，
		// 桥直接拿模型时 holder 是 null，NCardPlayQueue 会直接报错。改用游戏 AutoSlay 同款：
		// 先自己扣能量/星（SpendResources），再 CardCmd.AutoPlay（skipXCapture=true，X 值已由 SpendResources 记下）。
		Busy = true;
		TaskHelper.RunSafely(PlayCardAsync(card, target));
	}

	private static async Task PlayCardAsync(CardModel card, Creature? target)
	{
		string entry = card.Id.Entry;
		try
		{
			await card.SpendResources();
			await CardCmd.AutoPlay(new BlockingPlayerChoiceContext(), card, target, AutoPlayType.Default, skipXCapture: true);
			Log.Info($"[bridge] play_card {entry} resolved");
		}
		catch (Exception e)
		{
			LastError = "play_card failed: " + e.Message;
			Log.Error("[bridge] play_card failed: " + e);
		}
		finally
		{
			Busy = false;
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
		StateCollector.PressNode(target);
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

	public static void Fail(string message)
	{
		LastError = message;
		Log.Warn("[bridge] rejected action: " + message);
	}
}
