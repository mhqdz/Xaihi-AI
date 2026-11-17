using System.Collections.Generic;
using System.Text.Json;
using System.Text.Json.Serialization;
using System.Text.Encodings.Web;

namespace Sts2TextBridge;

/// <summary>
/// 桥写到 state.json 的结构。字段名按协议用 snake_case，null 字段不输出。
/// </summary>
public sealed class BridgeState
{
	[JsonPropertyName("seq")]
	public long Seq { get; set; }

	[JsonPropertyName("ts")]
	public double Ts { get; set; }

	[JsonPropertyName("scene")]
	public string Scene { get; set; } = "idle";

	[JsonPropertyName("floor")]
	public int? Floor { get; set; }

	[JsonPropertyName("player")]
	public PlayerInfo? Player { get; set; }

	[JsonPropertyName("hand")]
	public List<CardInfo>? Hand { get; set; }

	[JsonPropertyName("enemies")]
	public List<EnemyInfo>? Enemies { get; set; }

	[JsonPropertyName("relics")]
	public List<string>? Relics { get; set; }

	[JsonPropertyName("potions")]
	public List<PotionInfo>? Potions { get; set; }

	[JsonPropertyName("items")]
	public List<ItemInfo>? Items { get; set; }

	[JsonPropertyName("characters")]
	public List<CharacterInfo>? Characters { get; set; }

	[JsonPropertyName("rewards")]
	public List<RewardInfo>? Rewards { get; set; }

	[JsonPropertyName("nodes")]
	public List<MapNodeInfo>? Nodes { get; set; }

	[JsonPropertyName("event")]
	public EventInfo? Event { get; set; }

	[JsonPropertyName("options")]
	public List<OptionInfo>? Options { get; set; }

	[JsonPropertyName("cards")]
	public List<SelectCardInfo>? Cards { get; set; }

	/// <summary>压在最上层的模态弹窗（教程提示、确认框……）。底层场景的数据照常输出，只有这个字段变了。</summary>
	[JsonPropertyName("modal")]
	public string? Modal { get; set; }

	[JsonPropertyName("prompt")]
	public string? Prompt { get; set; }

	[JsonPropertyName("can_skip")]
	public bool? CanSkip { get; set; }

	[JsonPropertyName("result")]
	public string? Result { get; set; }

	[JsonPropertyName("score")]
	public int? Score { get; set; }

	/// <summary>当前屏幕上所有可点的东西——兜底用，保证不会因为漏枚举而卡死。</summary>
	[JsonPropertyName("clickables")]
	public List<ClickableInfo>? Clickables { get; set; }

	[JsonPropertyName("available_actions")]
	public List<Dictionary<string, object?>> AvailableActions { get; set; } = new();

	[JsonPropertyName("log")]
	public List<string>? Log { get; set; }

	[JsonPropertyName("last_error")]
	public string? LastError { get; set; }

	/// <summary>本次状态对应的界面截图（相对桥目录，如 shots/00012-combat.png）。只在界面变了的那次写。</summary>
	[JsonPropertyName("shot")]
	public string? Shot { get; set; }

	/// <summary>调试探针：只在不认识的场景里填，用来事后反推界面结构。</summary>
	[JsonPropertyName("probe")]
	public Dictionary<string, object?>? Probe { get; set; }
}

public sealed class PlayerInfo
{
	[JsonPropertyName("hp")] public int Hp { get; set; }
	[JsonPropertyName("max_hp")] public int MaxHp { get; set; }
	[JsonPropertyName("gold")] public int Gold { get; set; }
	[JsonPropertyName("energy")] public int? Energy { get; set; }
	[JsonPropertyName("max_energy")] public int? MaxEnergy { get; set; }
	[JsonPropertyName("block")] public int? Block { get; set; }
	[JsonPropertyName("powers")] public List<string>? Powers { get; set; }
}

public sealed class CardInfo
{
	[JsonPropertyName("index")] public int Index { get; set; }
	[JsonPropertyName("id")] public string Id { get; set; } = "";
	[JsonPropertyName("name")] public string Name { get; set; } = "";
	[JsonPropertyName("cost")] public int? Cost { get; set; }
	[JsonPropertyName("playable")] public bool Playable { get; set; }
	[JsonPropertyName("needs_target")] public bool NeedsTarget { get; set; }
	[JsonPropertyName("desc")] public string? Desc { get; set; }
}

public sealed class EnemyInfo
{
	[JsonPropertyName("index")] public int Index { get; set; }
	[JsonPropertyName("id")] public string Id { get; set; } = "";
	[JsonPropertyName("name")] public string Name { get; set; } = "";
	[JsonPropertyName("hp")] public int Hp { get; set; }
	[JsonPropertyName("max_hp")] public int MaxHp { get; set; }
	[JsonPropertyName("block")] public int Block { get; set; }
	[JsonPropertyName("intent")] public string? Intent { get; set; }
	[JsonPropertyName("powers")] public List<string>? Powers { get; set; }
}

public sealed class PotionInfo
{
	[JsonPropertyName("index")] public int Index { get; set; }
	[JsonPropertyName("id")] public string Id { get; set; } = "";
	[JsonPropertyName("name")] public string Name { get; set; } = "";
	[JsonPropertyName("usable")] public bool Usable { get; set; }
}

/// <summary>菜单项 / 商店条目共用。</summary>
public sealed class ItemInfo
{
	[JsonPropertyName("index")] public int Index { get; set; }
	[JsonPropertyName("id")] public string? Id { get; set; }
	[JsonPropertyName("label")] public string? Label { get; set; }
	[JsonPropertyName("name")] public string? Name { get; set; }
	[JsonPropertyName("enabled")] public bool? Enabled { get; set; }
	[JsonPropertyName("price")] public int? Price { get; set; }
	[JsonPropertyName("kind")] public string? Kind { get; set; }
}

public sealed class CharacterInfo
{
	[JsonPropertyName("index")] public int Index { get; set; }
	[JsonPropertyName("id")] public string Id { get; set; } = "";
	[JsonPropertyName("name")] public string Name { get; set; } = "";
	[JsonPropertyName("locked")] public bool Locked { get; set; }
}

public sealed class RewardInfo
{
	[JsonPropertyName("index")] public int Index { get; set; }
	[JsonPropertyName("kind")] public string Kind { get; set; } = "";
	[JsonPropertyName("name")] public string Name { get; set; } = "";
	[JsonPropertyName("desc")] public string? Desc { get; set; }
}

public sealed class MapNodeInfo
{
	[JsonPropertyName("index")] public int Index { get; set; }
	[JsonPropertyName("row")] public int Row { get; set; }
	[JsonPropertyName("col")] public int Col { get; set; }
	[JsonPropertyName("kind")] public string Kind { get; set; } = "";
	[JsonPropertyName("leads_to")] public List<int>? LeadsTo { get; set; }
}

public sealed class EventInfo
{
	[JsonPropertyName("id")] public string Id { get; set; } = "";
	[JsonPropertyName("title")] public string Title { get; set; } = "";
	[JsonPropertyName("body")] public string? Body { get; set; }
}

public sealed class OptionInfo
{
	[JsonPropertyName("index")] public int Index { get; set; }
	[JsonPropertyName("text")] public string Text { get; set; } = "";
	[JsonPropertyName("disabled")] public bool Disabled { get; set; }
}

public sealed class SelectCardInfo
{
	[JsonPropertyName("index")] public int Index { get; set; }
	[JsonPropertyName("id")] public string Id { get; set; } = "";
	[JsonPropertyName("name")] public string Name { get; set; } = "";
	[JsonPropertyName("upgraded")] public bool Upgraded { get; set; }
}

public sealed class ClickableInfo
{
	[JsonPropertyName("index")] public int Index { get; set; }
	[JsonPropertyName("label")] public string Label { get; set; } = "";
	[JsonPropertyName("kind")] public string Kind { get; set; } = "";
}

public static class BridgeJson
{
	public static readonly JsonSerializerOptions Options = new()
	{
		WriteIndented = true,
		DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
		Encoder = JavaScriptEncoder.UnsafeRelaxedJsonEscaping,
	};
}
