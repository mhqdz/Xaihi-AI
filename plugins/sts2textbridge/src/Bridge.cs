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

	/// <summary>排障开关：STS2_BRIDGE_QUIET=1 时不轮询界面，只在收到动作时读一次。</summary>
	public static bool Quiet { get; private set; }

	private static long _seq;
	private static string _lastSig = "";
	private static bool _started;

	public static void Start()
	{
		if (_started)
		{
			return;
		}
		_started = true;
		Quiet = System.Environment.GetEnvironmentVariable("STS2_BRIDGE_QUIET") == "1";

		Dir = ResolveDir();
		Directory.CreateDirectory(Dir);
		StatePath = Path.Combine(Dir, "state.json");
		ActionPath = Path.Combine(Dir, "action.json");
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

	/// <summary>读一次界面；内容没变就不写盘。</summary>
	internal static void CollectAndWrite()
	{
		try
		{
			BridgeState state = StateCollector.Collect();
			string sig = JsonSerializer.Serialize(state, BridgeJson.Options);
			if (sig == _lastSig)
			{
				return;
			}
			_lastSig = sig;
			_seq++;
			state.Seq = _seq;
			state.Ts = Time.GetUnixTimeFromSystem();
			WriteAtomic(StatePath, JsonSerializer.Serialize(state, BridgeJson.Options));
		}
		catch (Exception e)
		{
			Log.Error("[bridge] tick failed: " + e);
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
	}
}
