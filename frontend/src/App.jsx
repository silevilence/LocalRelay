import {useEffect, useMemo, useState} from 'react';
import {
    CreateModel,
    CreateProvider,
    DeleteModel,
    DeleteProvider,
    ListModels,
    ListProviderPresets,
    ListProviders,
    RelayBaseURL,
    TokenStats,
    UpdateModel,
    UpdateProvider,
} from "../wailsjs/go/main/App";

const emptyProvider = {id: "", name: "", type: "openai", baseUrl: "", apiKey: "", capabilityConfig: ""};
const emptyModel = {id: "", providerId: "", name: "", capabilities: "", contextLength: 0, maxTokens: 0, enabled: true};

function App() {
    const [providers, setProviders] = useState([]);
    const [providerPresets, setProviderPresets] = useState([]);
    const [models, setModels] = useState([]);
    const [selectedProviderId, setSelectedProviderId] = useState("");
    const [selectedPresetId, setSelectedPresetId] = useState("");
    const [providerDraft, setProviderDraft] = useState(emptyProvider);
    const [modelDraft, setModelDraft] = useState(emptyModel);
    const [search, setSearch] = useState("");
    const [isAddingProvider, setIsAddingProvider] = useState(false);
    const [editingModel, setEditingModel] = useState(false);
    const [showModelForm, setShowModelForm] = useState(false);
    const [showKey, setShowKey] = useState(false);
    const [message, setMessage] = useState("正在加载本地配置…");
    const [relayBaseUrl, setRelayBaseUrl] = useState("");
    const [statsFilter, setStatsFilter] = useState({from: daysAgo(7), to: today(), providerId: "", modelId: ""});
    const [stats, setStats] = useState({points: [], calls: 0, inputTokens: 0, outputTokens: 0, cacheCreationInputTokens: 0, cacheReadInputTokens: 0});

    const selectedProvider = useMemo(
        () => providers.find((provider) => provider.id === selectedProviderId),
        [providers, selectedProviderId],
    );

    const visibleProviders = useMemo(() => {
        const needle = search.trim().toLowerCase();
        if (!needle) return providers;
        return providers.filter((provider) =>
            [provider.name, provider.id, provider.type].some((value) => value?.toLowerCase().includes(needle)),
        );
    }, [providers, search]);

    const groupedModels = useMemo(() => {
        return models.reduce((groups, model) => {
            const family = modelFamily(model.id);
            groups[family] = [...(groups[family] || []), model];
            return groups;
        }, {});
    }, [models]);

    useEffect(() => {
        refreshProviders();
        ListProviderPresets().then((items) => setProviderPresets(items || [])).catch(() => {});
        RelayBaseURL().then(setRelayBaseUrl).catch(() => {});
    }, []);

    useEffect(() => {
        refreshStats();
    }, [statsFilter]);

    useEffect(() => {
        if (selectedProvider) {
            setProviderDraft(providerToDraft(selectedProvider));
            setIsAddingProvider(false);
            setSelectedPresetId("");
        }
        refreshModels(selectedProviderId);
        setShowModelForm(false);
        setEditingModel(false);
        setModelDraft({...emptyModel, providerId: selectedProviderId});
    }, [selectedProviderId, selectedProvider]);

    async function refreshProviders() {
        try {
            const items = await ListProviders();
            setProviders(items || []);
            setSelectedProviderId((current) => items?.some((item) => item.id === current) ? current : items?.[0]?.id || "");
            setMessage(items?.length ? "配置已加载。" : "先新增一个模型平台。");
        } catch (error) {
            setMessage(`加载平台失败：${error}`);
        }
    }

    async function refreshModels(providerId = selectedProviderId) {
        try {
            setModels((await ListModels(providerId || "")) || []);
        } catch (error) {
            setMessage(`加载模型失败：${error}`);
        }
    }

    async function saveProvider(event) {
        event.preventDefault();
        if (!providerDraft.id || !providerDraft.name || !providerDraft.type || !providerDraft.baseUrl) {
            setMessage("平台 ID、名称、类型和 API 地址必填。");
            return;
        }
        const preset = providerPresets.find((item) => item.id === selectedPresetId);
        const payload = {...providerDraft, capabilityConfig: providerDraft.capabilityConfig || preset?.capabilityConfig || ""};
        try {
            if (isAddingProvider) {
                await CreateProvider(payload);
                for (const model of preset?.models || []) {
                    await CreateModel({...model, providerId: payload.id, enabled: model.enabled !== false});
                }
            } else {
                await UpdateProvider(payload);
            }
            await refreshProviders();
            setSelectedProviderId(payload.id);
            setIsAddingProvider(false);
            setSelectedPresetId("");
            setMessage(isAddingProvider ? `平台已添加${preset?.models?.length ? `，并导入 ${preset.models.length} 个预设模型。` : "。"}` : "平台配置已保存。");
        } catch (error) {
            setMessage(`保存平台失败：${error}`);
        }
    }

    async function removeProvider(provider) {
        if (!confirm(`删除模型平台「${provider.name}」？关联模型会一起删除。`)) return;
        try {
            await DeleteProvider(provider.id);
            setMessage("平台已删除。");
            await refreshProviders();
            await refreshModels("");
        } catch (error) {
            setMessage(`删除平台失败：${error}`);
        }
    }

    async function saveModel(event) {
        event.preventDefault();
        const payload = {
            ...modelDraft,
            providerId: modelDraft.providerId || selectedProviderId,
            contextLength: Number(modelDraft.contextLength) || 0,
            maxTokens: Number(modelDraft.maxTokens) || 0,
            enabled: modelDraft.enabled !== false,
        };
        if (!payload.providerId || !payload.id || !payload.name) {
            setMessage("模型所属平台、ID 和名称必填。");
            return;
        }
        try {
            editingModel ? await UpdateModel(payload) : await CreateModel(payload);
            await refreshModels(selectedProviderId);
            setModelDraft({...emptyModel, providerId: selectedProviderId});
            setEditingModel(false);
            setShowModelForm(false);
            setMessage(editingModel ? "模型已更新。" : "模型已添加。");
        } catch (error) {
            setMessage(`保存模型失败：${error}`);
        }
    }

    async function removeModel(model) {
        if (!confirm(`删除模型「${model.id}」？`)) return;
        try {
            await DeleteModel(model.providerId, model.id);
            await refreshModels(selectedProviderId);
            setMessage("模型已删除。");
        } catch (error) {
            setMessage(`删除模型失败：${error}`);
        }
    }

    function startAddProvider() {
        setProviderDraft(emptyProvider);
        setSelectedPresetId("");
        setIsAddingProvider(true);
        setSelectedProviderId("");
        setModels([]);
    }

    function applyProviderPreset(preset) {
        setProviderDraft({
            id: preset.id,
            name: preset.name,
            type: preset.type,
            baseUrl: preset.baseUrl,
            apiKey: providerDraft.apiKey || "",
            capabilityConfig: preset.capabilityConfig || "",
        });
        setSelectedPresetId(preset.id);
        setIsAddingProvider(true);
        setSelectedProviderId("");
        setModels([]);
        setMessage(`已套用「${preset.name}」预设，填入 API Key 后保存。`);
    }

    function startAddModel() {
        if (!selectedProviderId) {
            setMessage("请先选择一个模型平台。");
            return;
        }
        setModelDraft({...emptyModel, providerId: selectedProviderId});
        setEditingModel(false);
        setShowModelForm(true);
    }

    function editModel(model) {
        setModelDraft({...model, enabled: model.enabled !== false});
        setEditingModel(true);
        setShowModelForm(true);
    }

    async function refreshStats() {
        try {
            const result = await TokenStats({
                from: statsFilter.from ? `${statsFilter.from}T00:00:00Z` : "",
                to: statsFilter.to ? `${statsFilter.to}T23:59:59Z` : "",
                providerId: statsFilter.providerId,
                modelId: statsFilter.modelId,
            });
            setStats(result || {points: []});
        } catch (error) {
            setMessage(`加载统计失败：${error}`);
        }
    }

    const providerCount = providers.length;
    const activeProvider = isAddingProvider ? null : selectedProvider;

    return (
        <div className="flex h-screen overflow-hidden bg-[#141414] text-zinc-100">
            <aside className="flex w-[320px] shrink-0 flex-col border-r border-zinc-800 bg-[#151515] p-3">
                <label className="mb-4 flex items-center gap-2 rounded-2xl border border-zinc-800 bg-[#101010] px-4 py-3 text-zinc-500">
                    <span>⌕</span>
                    <input
                        className="min-w-0 flex-1 bg-transparent text-sm text-zinc-200 outline-none placeholder:text-zinc-600"
                        value={search}
                        onChange={(event) => setSearch(event.target.value)}
                        placeholder="搜索模型平台..."
                    />
                    <span className="text-zinc-500">♢</span>
                </label>

                <div className="min-h-0 flex-1 space-y-1 overflow-y-auto pr-1">
                    {visibleProviders.map((provider) => (
                        <button
                            key={provider.id}
                            className={`flex w-full items-center gap-3 rounded-2xl px-3 py-2.5 text-left transition ${
                                provider.id === selectedProviderId && !isAddingProvider
                                    ? "border border-zinc-700 bg-zinc-800/70"
                                    : "hover:bg-zinc-900"
                            }`}
                            type="button"
                            onClick={() => setSelectedProviderId(provider.id)}
                        >
                            <Avatar name={provider.name || provider.id} />
                            <span className="min-w-0 flex-1 truncate text-base font-semibold text-zinc-200">{provider.name}</span>
                            <span className="rounded-full border border-green-700/60 bg-green-950/70 px-2 py-0.5 text-xs font-bold text-green-400">ON</span>
                        </button>
                    ))}
                </div>

                <button
                    className="mt-3 rounded-xl border border-zinc-700 px-4 py-2.5 text-base font-semibold text-zinc-100 transition hover:bg-zinc-900"
                    type="button"
                    onClick={startAddProvider}
                >
                    ＋ 添加
                </button>
            </aside>

            <main className="min-w-0 flex-1 overflow-y-auto px-7 py-5">
                <div className="mx-auto max-w-7xl">
                    <div className="mb-4 flex items-center justify-between border-b border-zinc-800 pb-5">
                        <div className="flex min-w-0 items-center gap-3">
                            <h1 className="truncate text-xl font-bold">{isAddingProvider ? "添加模型平台" : activeProvider?.name || "选择模型平台"}</h1>
                            {!isAddingProvider && activeProvider && <span className="text-zinc-400">◎</span>}
                        </div>
                        <button
                            className="h-8 w-14 rounded-full bg-emerald-500 p-1"
                            type="button"
                            onClick={() => setMessage("启用开关暂未落库，后续接入路由开关时启用。")}
                        >
                            <span className="block h-6 w-6 translate-x-6 rounded-full bg-white" />
                        </button>
                    </div>

                    {(activeProvider || isAddingProvider) ? (
                        <>
                            <ProviderPanel
                                draft={providerDraft}
                                isAdding={isAddingProvider}
                                presets={providerPresets}
                                selectedPresetId={selectedPresetId}
                                showKey={showKey}
                                onToggleKey={() => setShowKey((value) => !value)}
                                onChange={(patch) => setProviderDraft({...providerDraft, ...patch})}
                                onApplyPreset={applyProviderPreset}
                                onSubmit={saveProvider}
                                onDelete={activeProvider ? () => removeProvider(activeProvider) : null}
                                onTest={() => setMessage("检测接口尚未实现；当前仅保存本地配置。")}
                            />

                            {!isAddingProvider && (
                                <section className="mt-7">
                                    <div className="mb-4 flex items-center justify-between">
                                        <div className="flex items-center gap-3">
                                            <h2 className="text-xl font-bold">模型</h2>
                                            <span className="rounded-full bg-zinc-800 px-2.5 py-1 text-sm text-zinc-400">{models.length}</span>
                                            <span className="text-zinc-500">⌁</span>
                                            <span className="text-zinc-500">⌕</span>
                                        </div>
                                        <div className="flex overflow-hidden rounded-xl border border-zinc-700">
                                            <button
                                                className="px-4 py-2 font-semibold text-zinc-100 hover:bg-zinc-900"
                                                type="button"
                                                onClick={() => setMessage("获取模型列表尚未接入上游 API；请先手动添加。")}
                                            >
                                                ⟳ 获取模型列表
                                            </button>
                                            <button className="border-l border-zinc-700 px-4 py-2 text-xl hover:bg-zinc-900" type="button" onClick={startAddModel}>＋</button>
                                        </div>
                                    </div>

                                    {showModelForm && (
                                        <ModelForm
                                            draft={modelDraft}
                                            editing={editingModel}
                                            onChange={(patch) => setModelDraft({...modelDraft, ...patch})}
                                            onSubmit={saveModel}
                                            onCancel={() => {
                                                setShowModelForm(false);
                                                setEditingModel(false);
                                            }}
                                        />
                                    )}

                                    <div className="space-y-4">
                                        {Object.entries(groupedModels).map(([family, items]) => (
                                            <ModelGroup key={family} family={family} models={items} onEdit={editModel} onDelete={removeModel} />
                                        ))}
                                        {!models.length && (
                                            <div className="rounded-2xl border border-dashed border-zinc-800 p-10 text-center text-zinc-500">
                                                这个平台还没有模型。点右上角 ＋ 添加。
                                            </div>
                                        )}
                                    </div>
                                </section>
                            )}

                            <StatsPanel
                                baseUrl={relayBaseUrl}
                                stats={stats}
                                filter={statsFilter}
                                providers={providers}
                                onChange={(patch) => setStatsFilter({...statsFilter, ...patch})}
                                onRefresh={refreshStats}
                            />
                        </>
                    ) : (
                        <div className="grid h-[70vh] place-items-center rounded-3xl border border-dashed border-zinc-800 text-zinc-500">
                            左侧选择一个模型平台，或点击「添加」创建。
                        </div>
                    )}

                    <div className="mt-6 rounded-xl border border-zinc-800 bg-zinc-950/50 px-4 py-3 text-sm text-zinc-400">
                        {message} · 当前平台 {providerCount} 个
                    </div>
                </div>
            </main>
        </div>
    );
}

function ProviderPanel({draft, isAdding, presets, selectedPresetId, showKey, onToggleKey, onChange, onApplyPreset, onSubmit, onDelete, onTest}) {
    return (
        <form className="space-y-7" onSubmit={onSubmit}>
            {isAdding && presets.length > 0 && (
                <div>
                    <div className="mb-3 flex items-center justify-between">
                        <h2 className="text-xl font-bold">从预设添加</h2>
                        <span className="text-sm text-zinc-500">会自动导入常用模型</span>
                    </div>
                    <div className="grid gap-3 md:grid-cols-4">
                        {presets.map((preset) => (
                            <button
                                key={preset.id}
                                className={`rounded-2xl border px-4 py-3 text-left transition ${
                                    selectedPresetId === preset.id
                                        ? "border-emerald-500 bg-emerald-950/30"
                                        : "border-zinc-800 bg-zinc-950/40 hover:bg-zinc-900"
                                }`}
                                type="button"
                                onClick={() => onApplyPreset(preset)}
                            >
                                <div className="font-bold text-zinc-100">{preset.name}</div>
                                <div className="mt-1 truncate text-xs text-zinc-500">{preset.baseUrl}</div>
                                <div className="mt-2 text-xs text-zinc-400">{preset.models?.length || 0} 个模型</div>
                            </button>
                        ))}
                    </div>
                </div>
            )}

            {isAdding && (
                <div className="grid gap-4 md:grid-cols-3">
                    <Field label="平台 ID" value={draft.id} onChange={(id) => onChange({id})} placeholder="right-code-gemini" />
                    <Field label="平台名称" value={draft.name} onChange={(name) => onChange({name})} placeholder="Right Code Gemini" />
                    <Field label="类型" value={draft.type} onChange={(type) => onChange({type})} placeholder="openai" />
                </div>
            )}

            <div>
                <div className="mb-3 flex items-center justify-between">
                    <h2 className="text-xl font-bold">API 密钥</h2>
                    <span className="text-xl text-zinc-400">⌘</span>
                </div>
                <div className="flex overflow-hidden rounded-xl border border-zinc-700 bg-[#151515]">
                    <input
                        className="min-w-0 flex-1 bg-transparent px-4 py-3 text-lg outline-none placeholder:text-zinc-600"
                        type={showKey ? "text" : "password"}
                        value={draft.apiKey}
                        onChange={(event) => onChange({apiKey: event.target.value})}
                        placeholder="sk-..."
                    />
                    <button className="border-l border-zinc-700 px-4 text-zinc-400 hover:bg-zinc-900" type="button" onClick={onToggleKey}>⌧</button>
                    <button className="border-l border-zinc-700 px-5 font-bold hover:bg-zinc-900" type="button" onClick={onTest}>检测</button>
                </div>
                <p className="mt-2 text-right text-sm text-zinc-500">多个密钥使用逗号分隔</p>
            </div>

            <div>
                <div className="mb-3 flex items-center justify-between">
                    <h2 className="text-xl font-bold">API 地址 <span className="text-sm text-zinc-500">ⓘ</span></h2>
                    <span className="text-xl text-zinc-400">⌘</span>
                </div>
                <input
                    className="w-full rounded-xl border border-zinc-700 bg-[#151515] px-4 py-3 text-lg outline-none placeholder:text-zinc-600"
                    value={draft.baseUrl}
                    onChange={(event) => onChange({baseUrl: event.target.value})}
                    placeholder="https://api.openai.com/v1"
                />
                <p className="mt-2 text-sm text-zinc-500">预览：{previewChatUrl(draft.baseUrl)}</p>
            </div>

            <label className="grid gap-1 text-sm font-semibold text-zinc-300">
                差异兼容配置 JSON
                <textarea
                    className="min-h-32 rounded-xl border border-zinc-700 bg-[#151515] px-3 py-2.5 font-mono text-xs text-zinc-100 outline-none placeholder:text-zinc-600"
                    value={draft.capabilityConfig}
                    onChange={(event) => onChange({capabilityConfig: event.target.value})}
                    placeholder='{"protocol":"openai_chat"}'
                />
            </label>

            <div className="flex gap-3">
                <button className="rounded-xl bg-zinc-100 px-5 py-2.5 font-bold text-zinc-950 hover:bg-white" type="submit">
                    {isAdding ? "创建平台" : "保存配置"}
                </button>
                {onDelete && (
                    <button className="rounded-xl border border-red-900/70 px-5 py-2.5 font-bold text-red-300 hover:bg-red-950/40" type="button" onClick={onDelete}>
                        删除平台
                    </button>
                )}
            </div>
        </form>
    );
}

function ModelForm({draft, editing, onChange, onSubmit, onCancel}) {
    return (
        <form className="mb-4 grid gap-3 rounded-2xl border border-zinc-800 bg-zinc-900/40 p-4 md:grid-cols-3" onSubmit={onSubmit}>
            <Field label="模型 ID" value={draft.id} disabled={editing} onChange={(id) => onChange({id})} placeholder="gemini-3-flash-preview" />
            <Field label="显示名称" value={draft.name} onChange={(name) => onChange({name})} placeholder="gemini-3-flash-preview" />
            <Field label="能力 JSON" value={draft.capabilities} onChange={(capabilities) => onChange({capabilities})} placeholder='{"tools":true}' />
            <Field label="上下文长度" type="number" value={draft.contextLength} onChange={(contextLength) => onChange({contextLength})} />
            <Field label="Max Tokens" type="number" value={draft.maxTokens} onChange={(maxTokens) => onChange({maxTokens})} />
            <label className="flex items-end gap-2 pb-2 text-sm font-semibold text-zinc-300">
                <input type="checkbox" checked={draft.enabled !== false} onChange={(event) => onChange({enabled: event.target.checked})} />
                对外提供
            </label>
            <div className="flex items-end gap-2">
                <button className="rounded-xl bg-zinc-100 px-4 py-2 font-bold text-zinc-950" type="submit">{editing ? "更新" : "添加"}</button>
                <button className="rounded-xl border border-zinc-700 px-4 py-2 font-bold text-zinc-300" type="button" onClick={onCancel}>取消</button>
            </div>
        </form>
    );
}

function ModelGroup({family, models, onEdit, onDelete}) {
    return (
        <div className="overflow-hidden rounded-2xl border border-zinc-800">
            <div className="flex items-center justify-between bg-zinc-800/70 px-6 py-4">
                <div className="flex items-center gap-4">
                    <span className="text-zinc-500">⌄</span>
                    <h3 className="text-xl font-bold">{family}</h3>
                </div>
                <span className="text-xl text-zinc-400">-</span>
            </div>
            <div className="divide-y divide-zinc-900 bg-[#151515]">
                {models.map((model) => (
                    <div key={`${model.providerId}/${model.id}`} className="flex items-center gap-4 px-6 py-4">
                        <span className="text-2xl">✦</span>
                        <div className="min-w-0 flex-1">
                            <div className="truncate text-xl font-semibold">{model.name || model.id}</div>
                            <div className="text-xs text-zinc-500">context {model.contextLength || 0} · max {model.maxTokens || 0} · route {model.providerId}/{model.id}</div>
                        </div>
                        <span className={`rounded-full px-2.5 py-1 text-xs font-bold ${model.enabled !== false ? "bg-green-950 text-green-400" : "bg-zinc-800 text-zinc-500"}`}>
                            {model.enabled !== false ? "OPEN" : "OFF"}
                        </span>
                        <CapabilityPills value={model.capabilities} />
                        <button className="text-zinc-400 hover:text-zinc-100" type="button" onClick={() => onEdit(model)}>◎</button>
                        <button className="text-xl text-zinc-400 hover:text-red-300" type="button" onClick={() => onDelete(model)}>-</button>
                    </div>
                ))}
            </div>
        </div>
    );
}

function StatsPanel({baseUrl, stats, filter, providers, onChange, onRefresh}) {
    const points = stats.points || [];
    return (
        <section className="mt-7 rounded-2xl border border-zinc-800 bg-zinc-950/30 p-5">
            <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
                <div>
                    <h2 className="text-xl font-bold">Token 统计</h2>
                    <p className="mt-1 text-sm text-zinc-500">本地入口：{baseUrl ? `${baseUrl}/v1/chat/completions` : "启动中…"}</p>
                </div>
                <div className="flex flex-wrap items-end gap-2">
                    <Field label="开始" type="date" value={filter.from} onChange={(from) => onChange({from})} />
                    <Field label="结束" type="date" value={filter.to} onChange={(to) => onChange({to})} />
                    <label className="grid gap-1 text-sm font-semibold text-zinc-300">
                        平台
                        <select className="rounded-xl border border-zinc-700 bg-[#151515] px-3 py-2.5 text-zinc-100 outline-none" value={filter.providerId} onChange={(event) => onChange({providerId: event.target.value})}>
                            <option value="">全部</option>
                            {providers.map((provider) => <option key={provider.id} value={provider.id}>{provider.name}</option>)}
                        </select>
                    </label>
                    <Field label="模型 ID" value={filter.modelId} onChange={(modelId) => onChange({modelId})} placeholder="gpt-4.1-mini" />
                    <button className="rounded-xl bg-zinc-100 px-4 py-2.5 font-bold text-zinc-950" type="button" onClick={onRefresh}>刷新</button>
                </div>
            </div>

            <div className="grid gap-3 md:grid-cols-5">
                <StatCard label="调用" value={stats.calls || 0} />
                <StatCard label="输入" value={stats.inputTokens || 0} />
                <StatCard label="输出" value={stats.outputTokens || 0} />
                <StatCard label="缓存写入" value={stats.cacheCreationInputTokens || 0} />
                <StatCard label="缓存命中" value={stats.cacheReadInputTokens || 0} />
            </div>

            <div className="mt-4 flex flex-wrap gap-3 text-xs text-zinc-400">
                <Legend color="bg-emerald-500" label="非缓存输入" />
                <Legend color="bg-sky-500" label="输出" />
                <Legend color="bg-amber-500" label="缓存写入" />
                <Legend color="bg-violet-500" label="缓存命中" />
            </div>

            <div className="mt-4 overflow-hidden rounded-xl border border-zinc-800">
                {points.length > 0 && <TokenTrendChart points={points} />}
                {!points.length && <div className="px-4 py-8 text-center text-zinc-500">暂无调用日志。</div>}
            </div>
        </section>
    );
}

function TokenTrendChart({points}) {
    const width = 720;
    const labelWidth = 110;
    const valueWidth = 130;
    const chartWidth = width - labelWidth - valueWidth;
    const rowHeight = 36;
    const height = points.length * rowHeight + 12;
    const maxTokens = Math.max(1, ...points.map((point) => tokenTotal(point)));
    return (
        <svg className="h-auto w-full" viewBox={`0 0 ${width} ${height}`} role="img" aria-label="Token 趋势图">
            {points.map((point, index) => {
                const y = index * rowHeight + 18;
                const parts = tokenParts(point);
                let x = labelWidth;
                return (
                    <g key={point.date}>
                        <text x="16" y={y + 5} fill="#a1a1aa" fontSize="13">{point.date}</text>
                        <rect x={labelWidth} y={y - 8} width={chartWidth} height="12" rx="6" fill="#27272a" />
                        {parts.map((part) => {
                            const w = Math.round((part.value / maxTokens) * chartWidth);
                            const rect = <rect key={part.name} x={x} y={y - 8} width={w} height="12" fill={part.color} />;
                            x += w;
                            return rect;
                        })}
                        <text x={width - 16} y={y + 5} fill="#d4d4d8" fontSize="13" textAnchor="end">
                            {tokenTotal(point)} tokens · {point.calls} 次
                        </text>
                    </g>
                );
            })}
        </svg>
    );
}

function tokenParts(point) {
    const cache = (point.cacheCreationInputTokens || 0) + (point.cacheReadInputTokens || 0);
    return [
        {name: "input", value: Math.max(0, (point.inputTokens || 0) - cache), color: "#10b981"},
        {name: "output", value: point.outputTokens || 0, color: "#0ea5e9"},
        {name: "cacheCreate", value: point.cacheCreationInputTokens || 0, color: "#f59e0b"},
        {name: "cacheRead", value: point.cacheReadInputTokens || 0, color: "#8b5cf6"},
    ].filter((part) => part.value > 0);
}

function tokenTotal(point) {
    return (point.inputTokens || 0) + (point.outputTokens || 0);
}

function Legend({color, label}) {
    return (
        <span className="inline-flex items-center gap-1">
            <span className={`h-2.5 w-2.5 rounded-full ${color}`} />
            {label}
        </span>
    );
}

function StatCard({label, value}) {
    return (
        <div className="rounded-xl border border-zinc-800 bg-[#151515] p-4">
            <div className="text-sm text-zinc-500">{label}</div>
            <div className="mt-1 text-2xl font-bold">{Number(value || 0).toLocaleString()}</div>
        </div>
    );
}

function CapabilityPills({value}) {
    const text = value || "";
    const pills = [
        ["👁", true, "bg-emerald-950 text-emerald-400"],
        ["🌐", text.includes("vision") || text.includes("image"), "bg-blue-950 text-blue-400"],
        ["☀", text.includes("stream"), "bg-indigo-950 text-indigo-300"],
        ["🔧", text.includes("tool") || text.includes("function"), "bg-orange-950 text-orange-400"],
    ];
    return (
        <div className="hidden gap-2 md:flex">
            {pills.filter(([, show]) => show).map(([icon,, color]) => (
                <span key={icon} className={`rounded-full px-3 py-1 text-xs ${color}`}>{icon}</span>
            ))}
        </div>
    );
}

function Field({label, value, onChange, type = "text", disabled = false, placeholder = ""}) {
    return (
        <label className="grid gap-1 text-sm font-semibold text-zinc-300">
            {label}
            <input
                className="rounded-xl border border-zinc-700 bg-[#151515] px-3 py-2.5 text-zinc-100 outline-none placeholder:text-zinc-600 disabled:cursor-not-allowed disabled:opacity-60"
                type={type}
                value={value}
                disabled={disabled}
                placeholder={placeholder}
                onChange={(event) => onChange(event.target.value)}
            />
        </label>
    );
}

function Avatar({name}) {
    return (
        <span className="grid h-9 w-9 shrink-0 place-items-center rounded-full bg-sky-500 text-lg font-bold text-zinc-950">
            {(name || "?").slice(0, 1).toUpperCase()}
        </span>
    );
}

function providerToDraft(provider) {
    return {
        id: provider.id,
        name: provider.name,
        type: provider.type,
        baseUrl: provider.baseUrl,
        apiKey: provider.apiKey || "",
        capabilityConfig: provider.capabilityConfig || "",
    };
}

function modelFamily(id = "") {
    const parts = id.split("-").filter(Boolean);
    if (parts.length >= 2) return `${parts[0]}-${parts[1]}`;
    return parts[0] || "models";
}

function previewChatUrl(baseUrl = "") {
    const trimmed = baseUrl.trim().replace(/\/+$/, "");
    return trimmed ? `${trimmed}/chat/completions` : "填写 API 地址后显示";
}

function today() {
    return new Date().toISOString().slice(0, 10);
}

function daysAgo(days) {
    const date = new Date();
    date.setDate(date.getDate() - days);
    return date.toISOString().slice(0, 10);
}

export default App
