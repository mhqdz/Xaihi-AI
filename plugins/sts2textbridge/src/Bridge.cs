using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;
using Godot;
using MegaCrit.Sts2.Core.Logging;
using MegaCrit.Sts2.Core.Modding;
using MegaCrit.Sts2.Core.Nodes;

namespace Sts2TextBridge;

[ModInitializer(nameof(Init))]
public static class BridgeMod
{
	public static void Init()
	{
		string? on = System.Environment.GetEnvironmentVariable("STS2_BRIDGE");
		if (on != "1")
		{
			Log.Info("[bridge] disabled (set STS2_BRIDGE=1 to enable)");
			return;
		}
		try
		{
			Bridge.Start();
		}
		catch (Exception e)
		{
			Log.Error("[bridge] init failed: " + e);
		}
	}
}

public static class Bridge
{
	private const double PollInterval = 0.1;

	public static string Dir = "";
	public static string StatePath = "";
	public static string ActionPath = "";

	/// <summary>排障开关：STS2_BRIDGE_TRACE=1 时把每个阶段写进 trace.log，用来定位原生崩溃点。</summary>
	public static bool Trace { get; private set; }

	/// <summary>排障开关：STS2_BRIDGE_QUIET=1 时不轮询界面，只在收到动作时读一次。</summary>
	public static bool Quiet { get; private set; }

	private static long _seq;
	private static string _lastSig = "";
	private static bool _started;

	/// <summary>界面指纹：scene + overlay + modal。只用来判断"要不要截图"，不用来限制写盘。</summary>
	private static string _lastUiKey = "";

	/// <summary>上一次真正写盘的时间（秒），10 秒兜底心跳用。</summary>
	private static double _lastWriteAt;

	/// <summary>待截图的界面指纹；非空表示下一帧抓一张。</summary>
	private static string _pendingShot = "";

	/// <summary>截图开关：STS2_BRIDGE_SHOTS=0 关掉。</summary>
	public static bool Shots { get; private set; } = true;

	public static string ShotsDir = "";

	/// <summary>调试开关：STS2_BRIDGE_GOD=1 时每场战斗给玩家 +999 力量/+999 敏捷（只为跑通流程，不是玩法）。</summary>
	public static bool God { get; private set; }

	public static void Start()
	{
		if (_started)
		{
			return;
		}
		_started = true;
		Quiet = System.Environment.GetEnvironmentVariable("STS2_BRIDGE_QUIET") == "1";
		Trace = System.Environment.GetEnvironmentVariable("STS2_BRIDGE_TRACE") == "1";

		Dir = ResolveDir();
		Directory.CreateDirectory(Dir);
		StatePath = Path.Combine(Dir, "state.json");
		ActionPath = Path.Combine(Dir, "action.json");
		Shots = System.Environment.GetEnvironmentVariable("STS2_BRIDGE_SHOTS") != "0";
		God = System.Environment.GetEnvironmentVariable("STS2_BRIDGE_GOD") == "1";
		if (God)
		{
			Log.Warn("[bridge] GOD MODE ON (+999 str/dex per combat)");
		}
		ShotsDir = Path.Combine(Dir, "shots");
		if (Shots)
		{
			try
			{
				Directory.CreateDirectory(ShotsDir);
			}
			catch (Exception e)
			{
				Log.Warn("[bridge] shots dir failed: " + e.Message);
				Shots = false;
			}
		}
		TryDelete(ActionPath);
		Log.Info("[bridge] data dir = " + Dir);

		if (!(Engine.GetMainLoop() is SceneTree tree))
		{
			Log.Error("[bridge] no SceneTree; ticker not installed");
			return;
		}

		BridgeTicker ticker = new BridgeTicker();
		ticker.Name = "Sts2TextBridgeTicker";
		tree.Root.CallDeferred(Node.MethodName.AddChild, ticker);

		Tick();
		Log.Info("[bridge] bridge ready");
	}

	private static string ResolveDir()
	{
		string? over = System.Environment.GetEnvironmentVariable("STS2_BRIDGE_DATA");
		if (!string.IsNullOrEmpty(over))
		{
			return over!;
		}
		string? xdg = System.Environment.GetEnvironmentVariable("XDG_DATA_HOME");
		if (!string.IsNullOrEmpty(xdg))
		{
			return Path.Combine(xdg!, "slaythespire2", "bridge");
		}
		string home = System.Environment.GetFolderPath(System.Environment.SpecialFolder.UserProfile);
		if (string.IsNullOrEmpty(home))
		{
			home = "/root";
		}
		return Path.Combine(home, ".local", "share", "slaythespire2", "bridge");
	}

	internal static void Tick()
	{
		bool didAction = false;
		try
		{
			didAction = ActionExecutor.ProcessFile(ActionPath);
		}
		catch (Exception e)
		{
			Log.Error("[bridge] action processing failed: " + e);
		}

		if (Quiet && !didAction)
		{
			return;
		}

		CollectAndWrite();
	}

	/// <summary>
	/// 读一次界面。写盘规则（见 docs/STS2-BRIDGE.md 的「触发策略」）：
	/// 内容变了才写（seq++）；内容没变时只有超过 10 秒才补写一次（只更新 ts，不动 seq）。
	/// 截图规则：只看界面指纹（scene/overlay/modal）变没变，变了就排一张，不看数据。
	/// </summary>
	internal static void CollectAndWrite()
	{
		Mark("collect.start");
		try
		{
			BridgeState state = StateCollector.Collect();
			string uiKey = UiKeyOf(state);
			string sig = JsonSerializer.Serialize(state, BridgeJson.Options);
			double now = Time.GetUnixTimeFromSystem();
			bool uiChanged = uiKey != _lastUiKey;
			bool dataChanged = sig != _lastSig;

			if (uiChanged && Shots)
			{
				// 宁可多截，不可漏截：新界面就排一张，不做去重
				_pendingShot = uiKey;
			}

			if (!dataChanged)
			{
				// 10 秒兜底：界面没动、数据也没动，只把 ts 往前推一格，让等待方能判断桥还活着
				if (now - _lastWriteAt < 10.0)
				{
					return;
				}
				state.Seq = _seq;
				state.Ts = now;
				if (state.Probe != null)
				{
					state.Probe["idle_s"] = Math.Round(now - _lastWriteAt, 1);
				}
				WriteAtomic(StatePath, JsonSerializer.Serialize(state, BridgeJson.Options));
				_lastWriteAt = now;
				Mark("write.idle");
				return;
			}

			_lastSig = sig;
			_lastUiKey = uiKey;
			_seq++;
			state.Seq = _seq;
			state.Ts = now;
			if (Shots && _pendingShot.Length > 0)
			{
				state.Shot = ShotNameFor(_seq, uiKey);
			}
			WriteAtomic(StatePath, JsonSerializer.Serialize(state, BridgeJson.Options));
			_lastWriteAt = now;
			Mark("write.seq=" + _seq + " " + uiKey);
		}
		catch (Exception e)
		{
			Log.Error("[bridge] tick failed: " + e);
		}
	}

	/// <summary>界面指纹：只有这三样变了才算"新界面"。</summary>
	private static string UiKeyOf(BridgeState state)
	{
		string overlay = "";
		string modal = "";
		if (state.Probe != null)
		{
			if (state.Probe.TryGetValue("overlay", out object? o) && o != null)
			{
				overlay = o.ToString() ?? "";
			}
			if (state.Probe.TryGetValue("modal", out object? m) && m != null)
			{
				modal = m.ToString() ?? "";
			}
		}
		return state.Scene + "|" + overlay + "|" + modal;
	}

	private static string ShotNameFor(long seq, string uiKey)
	{
		string scene = uiKey.Split('|')[0];
		foreach (char bad in new[] { '/', '\\', ':', ' ', '|' })
		{
			scene = scene.Replace(bad, '_');
		}
		return $"shots/{seq:00000}-{scene}.png";
	}

	/// <summary>下一帧抓图（在主线程 _Process 里调）。抓完把文件名写回 state.json 的 shot 字段。</summary>
	internal static void CapturePendingShot()
	{
		if (_pendingShot.Length == 0 || !Shots)
		{
			return;
		}
		string name = ShotNameFor(_seq, _pendingShot);
		_pendingShot = "";
		try
		{
			SceneTree? tree = Engine.GetMainLoop() as SceneTree;
			Viewport? vp = tree?.Root;
			if (vp == null)
			{
				return;
			}
			Image? img = vp.GetTexture()?.GetImage();
			if (img == null)
			{
				return;
			}
			if (img.GetWidth() > 1600)
			{
				int h = (int)Math.Round(img.GetHeight() * 1600.0 / img.GetWidth());
				img.Resize(1600, h, Image.Interpolation.Bilinear);
			}
			string path = Path.Combine(Dir, name);
			Error err = img.SavePng(path);
			Mark("shot " + name + " -> " + err);
		}
		catch (Exception e)
		{
			Log.Warn("[bridge] screenshot failed: " + e.Message);
		}
	}

	internal static void Mark(string what)
	{
		if (!Trace)
		{
			return;
		}
		try
		{
			File.AppendAllText(Path.Combine(Dir, "trace.log"), what + "\n");
		}
		catch (Exception)
		{
		}
	}

	internal static void WriteAtomic(string path, string text)
	{
		string tmp = path + ".tmp";
		File.WriteAllText(tmp, text);
		File.Move(tmp, path, overwrite: true);
	}

	internal static void TryDelete(string path)
	{
		try
		{
			if (File.Exists(path))
			{
				File.Delete(path);
			}
		}
		catch (Exception e)
		{
			Log.Warn("[bridge] could not delete " + path + ": " + e.Message);
		}
	}

	public static double PollIntervalSeconds => PollInterval;
}

public partial class BridgeTicker : Node
{
	private double _acc;

	public override void _Process(double delta)
	{
		_acc += delta;
		if (_acc < Bridge.PollIntervalSeconds)
		{
			return;
		}
		_acc = 0.0;
		Bridge.Tick();
		Bridge.CapturePendingShot();
	}
}
