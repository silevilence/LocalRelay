import {useEffect, useMemo, useRef, useState} from 'react';
import * as echarts from 'echarts';
import {
    CallLogs,
    CreateAPIKey,
    CreateModel,
    CreateProvider,
    DeleteAPIKey,
    DeleteModel,
    DeleteProvider,
    ListAPIKeys,
    ListModels,
    ListProviderPresets,
    ListProviders,
    RelayBaseURL,
    TokenStatApps,
    TokenStatModels,
    TokenStatRows,
    TokenStats,
    TokenTrend,
    TestProviderModel,
    UpdateAPIKey,
    UpdateModel,
    UpdateProvider,
} from "../wailsjs/go/main/App";

const emptyProvider = {id: "", name: "", type: "openai", baseUrl: "", apiKey: "", capabilityConfig: ""};
const emptyModel = {id: "", providerId: "", name: "", capabilities: "", contextLength: 0, maxTokens: 0, enabled: true};
const emptyApiKey = {id: 0, name: "", description: ""};
const thinkingRequestFieldOptions = ["thinking", "enable_thinking", "thinking_budget"];
const reasoningValueOptions = ["none", "minimal", "low", "medium", "high", "xhigh", "max"];
const reasoningMapSources = ["none", "minimal", "low", "medium", "high", "xhigh"];
const providerProtocolOptions = [
    ["openai_chat", "OpenAI Chat Completions"],
    ["anthropic_messages", "Anthropic Messages"],
    ["gemini", "Google Gemini"],
    ["openai_responses", "OpenAI Responses"],
];
const providerTypeOptions = [
    ["openai", "OpenAI"],
    ["openai-compatible", "OpenAI 兼容"],
    ["deepseek", "DeepSeek"],
    ["siliconflow", "硅基流动"],
    ["anthropic", "Anthropic"],
    ["gemini", "Google Gemini"],
    ["openai-responses", "OpenAI Responses"],
];

function App() {
    const [providers, setProviders] = useState([]);
    const [providerPresets, setProviderPresets] = useState([]);
    const [models, setModels] = useState([]);
    const [apiKeys, setApiKeys] = useState([]);
    const [apiKeyDraft, setApiKeyDraft] = useState(emptyApiKey);
    const [editingApiKey, setEditingApiKey] = useState(false);
    const [showApiKeyForm, setShowApiKeyForm] = useState(false);
    const [visibleApiKeys, setVisibleApiKeys] = useState({});
    const [selectedProviderId, setSelectedProviderId] = useState("");
    const [selectedPresetId, setSelectedPresetId] = useState("");
    const [providerDraft, setProviderDraft] = useState(emptyProvider);
    const [modelDraft, setModelDraft] = useState(emptyModel);
    const [search, setSearch] = useState("");
    const [page, setPage] = useState("providers");
    const [isAddingProvider, setIsAddingProvider] = useState(false);
    const [editingModel, setEditingModel] = useState(false);
    const [showModelForm, setShowModelForm] = useState(false);
    const [showProviderTest, setShowProviderTest] = useState(false);
    const [testModelId, setTestModelId] = useState("");
    const [testResult, setTestResult] = useState(null);
    const [isTestingProvider, setIsTestingProvider] = useState(false);
    const [showPresetPicker, setShowPresetPicker] = useState(false);
    const [presetSearch, setPresetSearch] = useState("");
    const [showKey, setShowKey] = useState(false);
    const [message, setMessage] = useState("正在加载本地配置…");
    const [toast, setToast] = useState("");
    const [relayBaseUrl, setRelayBaseUrl] = useState("");
    const [statsFilter, setStatsFilter] = useState({from: daysAgo(7), to: today(), providerId: "", modelId: "", appName: ""});
    const [statsModelOptions, setStatsModelOptions] = useState([]);
    const [statsAppOptions, setStatsAppOptions] = useState([]);
    const [stats, setStats] = useState({points: [], calls: 0, inputTokens: 0, outputTokens: 0, cacheCreationInputTokens: 0, cacheReadInputTokens: 0});
    const [statRows, setStatRows] = useState({provider: [], model: [], app: []});
    const [trendRows, setTrendRows] = useState([]);
    const [logPage, setLogPage] = useState({items: [], total: 0});
    const [logPageNum, setLogPageNum] = useState(1);
    const [statsTable, setStatsTable] = useState("logs");
    const [statsChart, setStatsChart] = useState("appPie");
    const [statsGrain, setStatsGrain] = useState("day");
    const [statsStackBy, setStatsStackBy] = useState("app");
    const [statsMetric, setStatsMetric] = useState("total");

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

    const filteredPresets = useMemo(() => {
        const needle = presetSearch.trim().toLowerCase();
        if (!needle) return providerPresets;
        return providerPresets.filter((preset) =>
            [
                preset.id,
                preset.name,
                preset.type,
                preset.baseUrl,
                ...(preset.models || []).flatMap((model) => [model.id, model.name]),
            ].some((value) => value?.toLowerCase().includes(needle)),
        );
    }, [providerPresets, presetSearch]);

    useEffect(() => {
        refreshProviders();
        refreshAPIKeys();
        ListProviderPresets().then((items) => setProviderPresets(items || [])).catch(() => {});
        RelayBaseURL().then(setRelayBaseUrl).catch(() => {});
    }, []);

    useEffect(() => {
        refreshStats();
    }, [statsFilter, statsGrain, statsStackBy, logPageNum]);

    useEffect(() => {
        refreshStatsModelOptions();
        refreshStatsAppOptions();
    }, [statsFilter.from, statsFilter.to, statsFilter.providerId, statsFilter.modelId]);

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

    async function refreshAPIKeys() {
        try {
            setApiKeys((await ListAPIKeys()) || []);
        } catch (error) {
            setMessage(`加载 API Key 失败：${error}`);
        }
    }

    async function saveAPIKey(event) {
        event.preventDefault();
        if (!apiKeyDraft.name.trim()) {
            setMessage("应用名称必填。");
            return;
        }
        try {
            editingApiKey ? await UpdateAPIKey(apiKeyDraft) : await CreateAPIKey(apiKeyDraft);
            await refreshAPIKeys();
            setApiKeyDraft(emptyApiKey);
            setEditingApiKey(false);
            setShowApiKeyForm(false);
            const success = editingApiKey ? "API Key 已更新。" : "API Key 已生成。";
            setMessage(success);
            notify(success);
        } catch (error) {
            setMessage(`保存 API Key 失败：${error}`);
        }
    }

    async function removeAPIKey(item) {
        if (!confirm(`删除应用「${item.name}」的 API Key？历史日志会保留应用名称。`)) return;
        try {
            await DeleteAPIKey(item.id);
            await refreshAPIKeys();
            setMessage("API Key 已删除。");
        } catch (error) {
            setMessage(`删除 API Key 失败：${error}`);
        }
    }

    async function copyText(text) {
        try {
            await navigator.clipboard.writeText(text);
            notify("已复制");
        } catch {
            setMessage("复制失败，请手动选择文本复制。");
        }
    }

    async function saveProvider(event) {
        event.preventDefault();
        if (!providerDraft.id || !providerDraft.name || !providerDraft.type || !providerDraft.baseUrl) {
            setMessage("平台 ID、名称、供应商分类和 API 地址必填。");
            return;
        }
        const preset = providerPresets.find((item) => item.id === selectedPresetId);
        const payload = {...providerDraft, capabilityConfig: providerDraft.capabilityConfig || preset?.capabilityConfig || ""};
        try {
            if (isAddingProvider) {
                await CreateProvider(payload);
                for (const model of preset?.models || []) {
                    const modelPayload = {...model, providerId: payload.id, enabled: model.enabled !== false};
                    try {
                        await CreateModel(modelPayload);
                    } catch {
                        await UpdateModel(modelPayload);
                    }
                }
            } else {
                await UpdateProvider(payload);
            }
            await refreshProviders();
            setSelectedProviderId(payload.id);
            setIsAddingProvider(false);
            setSelectedPresetId("");
            const success = isAddingProvider ? `平台已添加${preset?.models?.length ? `，并导入 ${preset.models.length} 个预设模型。` : "。"}` : "平台配置已保存。";
            setMessage(success);
            notify(success);
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
            const success = editingModel ? "模型已更新。" : "模型已添加。";
            setMessage(success);
            notify(success);
        } catch (error) {
            setMessage(`保存模型失败：${error}`);
        }
    }

    function notify(text) {
        setToast(text);
        window.setTimeout(() => setToast(""), 2400);
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
        setPage("providers");
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
        setShowPresetPicker(false);
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

    function openProviderTest() {
        if (isAddingProvider || !activeProvider) {
            setMessage("请先保存平台，再选择模型检测。");
            notify("请先保存平台");
            return;
        }
        const enabledModels = models.filter((model) => model.enabled !== false);
        if (!enabledModels.length) {
            setMessage("至少配置并启用一个模型后才能检测。");
            notify("请先配置模型");
            return;
        }
        setTestModelId(enabledModels[0].id);
        setTestResult(null);
        setShowProviderTest(true);
    }

    async function runProviderTest() {
        if (!activeProvider || !testModelId) return;
        setIsTestingProvider(true);
        setTestResult(null);
        try {
            const result = await TestProviderModel(activeProvider.id, testModelId);
            setTestResult({ok: true, ...result});
            notify("检测通过");
        } catch (error) {
            setTestResult({ok: false, error: String(error)});
        } finally {
            setIsTestingProvider(false);
        }
    }

    async function refreshStats() {
        const filter = statsFilterPayload(statsFilter);
        try {
            const [summary, providersRows, modelsRows, appsRows, trend, logs] = await Promise.all([
                TokenStats(filter),
                TokenStatRows(filter, "provider"),
                TokenStatRows(filter, "model"),
                TokenStatRows(filter, "app"),
                TokenTrend(filter, statsGrain, statsStackBy),
                CallLogs(filter, logPageNum, 50),
            ]);
            setStats(summary || {points: []});
            setStatRows({provider: providersRows || [], model: modelsRows || [], app: appsRows || []});
            setTrendRows(trend || []);
            setLogPage(logs || {items: [], total: 0});
        } catch (error) {
            setMessage(`加载统计失败：${error}`);
        }
    }

    async function refreshStatsModelOptions() {
        try {
            const options = await TokenStatModels({...statsFilterPayload(statsFilter), modelId: ""});
            const items = options || [];
            setStatsModelOptions(items);
            setStatsFilter((current) => current.modelId && !items.includes(current.modelId) ? {...current, modelId: ""} : current);
        } catch (error) {
            setMessage(`加载模型筛选项失败：${error}`);
        }
    }

    async function refreshStatsAppOptions() {
        try {
            const options = await TokenStatApps({...statsFilterPayload(statsFilter), appName: ""});
            const items = options || [];
            setStatsAppOptions(items);
            setStatsFilter((current) => current.appName && !items.includes(current.appName) ? {...current, appName: ""} : current);
        } catch (error) {
            setMessage(`加载应用筛选项失败：${error}`);
        }
    }

    const providerCount = providers.length;
    const activeProvider = isAddingProvider ? null : selectedProvider;

    return (
        <div className="flex h-screen flex-col overflow-hidden bg-[#141414] text-zinc-100">
            <header className="flex shrink-0 items-center justify-between border-b border-zinc-800 bg-[#151515] px-6 py-3">
                <div>
                    <h1 className="text-lg font-bold">LocalRelay</h1>
                    <p className="text-xs text-zinc-500">本地模型网关配置</p>
                </div>
                <div className="grid w-[26rem] grid-cols-3 gap-2">
                    <NavButton active={page === "providers"} onClick={() => setPage("providers")}>配置</NavButton>
                    <NavButton active={page === "apikeys"} onClick={() => setPage("apikeys")}>API Key</NavButton>
                    <NavButton active={page === "stats"} onClick={() => setPage("stats")}>Token 统计</NavButton>
                </div>
            </header>

            {page === "apikeys" ? (
                <main className="min-h-0 flex-1 overflow-y-auto px-7 py-5">
                    <div className="mx-auto max-w-7xl">
                        <APIKeyPanel
                            items={apiKeys}
                            draft={apiKeyDraft}
                            editing={editingApiKey}
                            showForm={showApiKeyForm}
                            visibleKeys={visibleApiKeys}
                            onNew={() => {
                                setApiKeyDraft(emptyApiKey);
                                setEditingApiKey(false);
                                setShowApiKeyForm(true);
                            }}
                            onEdit={(item) => {
                                setApiKeyDraft({id: item.id, name: item.name, description: item.description || ""});
                                setEditingApiKey(true);
                                setShowApiKeyForm(true);
                            }}
                            onDelete={removeAPIKey}
                            onCopy={copyText}
                            onToggleVisible={(id) => setVisibleApiKeys((current) => ({...current, [id]: !current[id]}))}
                        />
                        <StatusMessage message={message} providerCount={providerCount} />
                    </div>
                </main>
            ) : page === "stats" ? (
                <main className="min-h-0 flex-1 overflow-y-auto px-7 py-5">
                    <div className="mx-auto max-w-7xl">
                        <StatsPanel
                            baseUrl={relayBaseUrl}
                            stats={stats}
                            statRows={statRows}
                            trendRows={trendRows}
                            logPage={logPage}
                            logPageNum={logPageNum}
                            table={statsTable}
                            chart={statsChart}
                            grain={statsGrain}
                            stackBy={statsStackBy}
                            metric={statsMetric}
                            filter={statsFilter}
                            providers={providers}
                            modelOptions={statsModelOptions}
                            appOptions={statsAppOptions}
                            onChange={(patch) => {
                                const nextFilter = {...statsFilter, ...patch};
                                const nextGrain = coerceGrain(nextFilter, statsGrain);
                                if (nextGrain !== statsGrain) {
                                    setStatsGrain(nextGrain);
                                    setMessage(nextGrain === "hour" ? "时间范围不足，已切换为按小时统计。" : "时间范围不足，已切换为按天统计。");
                                }
                                setLogPageNum(1);
                                setStatsFilter(nextFilter);
                            }}
                            onTable={setStatsTable}
                            onChart={setStatsChart}
                            onGrain={(grain) => {
                                if (!grainAllowed(statsFilter, grain)) {
                                    setMessage(grain === "week" ? "按周统计要求时间范围至少 7 天。" : "按天统计要求时间范围至少 1 天。");
                                    return;
                                }
                                setStatsGrain(grain);
                            }}
                            onStackBy={setStatsStackBy}
                            onMetric={setStatsMetric}
                            onPage={setLogPageNum}
                            onRefresh={refreshStats}
                        />
                        <StatusMessage message={message} providerCount={providerCount} />
                    </div>
                </main>
            ) : (
                <div className="flex min-h-0 flex-1">
                    <aside className="flex w-[320px] shrink-0 flex-col border-r border-zinc-800 bg-[#151515] p-3">
                        <label className="mb-4 flex items-center gap-2 rounded-2xl border border-zinc-800 bg-[#101010] px-4 py-3 text-zinc-500">
                            <SearchIcon />
                            <input
                                className="min-w-0 flex-1 bg-transparent text-sm text-zinc-200 outline-none placeholder:text-zinc-600"
                                value={search}
                                onChange={(event) => setSearch(event.target.value)}
                                placeholder="搜索模型平台..."
                            />
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
                            {(activeProvider || isAddingProvider) ? (
                                <>
                                    <div className="mb-4 flex items-center justify-between border-b border-zinc-800 pb-5">
                                        <div className="flex min-w-0 items-center gap-3">
                                            <h1 className="truncate text-xl font-bold">{isAddingProvider ? "添加模型平台" : activeProvider?.name || "选择模型平台"}</h1>
                                            {!isAddingProvider && activeProvider && <span className="rounded-full border border-zinc-700 px-2 py-0.5 text-xs text-zinc-400">ID: {activeProvider.id}</span>}
                                            {!isAddingProvider && activeProvider && <span className="rounded-full border border-zinc-700 px-2 py-0.5 text-xs text-zinc-400">{activeProvider.type}</span>}
                                        </div>
                                        <button
                                            className="h-8 w-14 rounded-full bg-emerald-500 p-1"
                                            type="button"
                                            onClick={() => setMessage("启用开关暂未落库，后续接入路由开关时启用。")}
                                            aria-label="平台启用状态"
                                        >
                                            <span className="block h-6 w-6 translate-x-6 rounded-full bg-white" />
                                        </button>
                                    </div>

                                    <ProviderPanel
                                        draft={providerDraft}
                                        isAdding={isAddingProvider}
                                        presets={providerPresets}
                                        filteredPresets={filteredPresets}
                                        selectedPresetId={selectedPresetId}
                                        showPresetPicker={showPresetPicker}
                                        presetSearch={presetSearch}
                                        showKey={showKey}
                                        onToggleKey={() => setShowKey((value) => !value)}
                                        onChange={(patch) => setProviderDraft({...providerDraft, ...patch})}
                                        onApplyPreset={applyProviderPreset}
                                        onOpenPresetPicker={() => setShowPresetPicker(true)}
                                        onClosePresetPicker={() => setShowPresetPicker(false)}
                                        onPresetSearch={setPresetSearch}
                                        onSubmit={saveProvider}
                                        onDelete={activeProvider ? () => removeProvider(activeProvider) : null}
                                        onTest={openProviderTest}
                                    />

                                    {!isAddingProvider && (
                                        <section className="mt-7">
                                            <div className="mb-4 flex items-center justify-between">
                                                <div className="flex items-center gap-3">
                                                    <h2 className="text-xl font-bold">模型</h2>
                                                    <span className="rounded-full bg-zinc-800 px-2.5 py-1 text-sm text-zinc-400">{models.length}</span>
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
                                </>
                            ) : (
                                <div className="grid h-[70vh] place-items-center rounded-3xl border border-dashed border-zinc-800 text-zinc-500">
                                    左侧选择一个模型平台，或点击「添加」创建。
                                </div>
                            )}

                            <StatusMessage message={message} providerCount={providerCount} />
                        </div>
                    </main>
                </div>
            )}

            {showModelForm && (
                <Modal title={editingModel ? "编辑模型" : "添加模型"} onClose={() => {
                    setShowModelForm(false);
                    setEditingModel(false);
                }}>
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
                </Modal>
            )}
            {showProviderTest && activeProvider && (
                <Modal title="检测 API Key" onClose={() => setShowProviderTest(false)}>
                    <ProviderTestForm
                        provider={activeProvider}
                        models={models.filter((model) => model.enabled !== false)}
                        modelId={testModelId}
                        result={testResult}
                        testing={isTestingProvider}
                        onModelChange={setTestModelId}
                        onRun={runProviderTest}
                    />
                </Modal>
            )}
            {showApiKeyForm && (
                <Modal title={editingApiKey ? "编辑 API Key" : "新增 API Key"} onClose={() => setShowApiKeyForm(false)}>
                    <APIKeyForm
                        draft={apiKeyDraft}
                        editing={editingApiKey}
                        onChange={(patch) => setApiKeyDraft({...apiKeyDraft, ...patch})}
                        onSubmit={saveAPIKey}
                        onCancel={() => setShowApiKeyForm(false)}
                    />
                </Modal>
            )}
            {toast && <Toast>{toast}</Toast>}
        </div>
    );
}

function ProviderPanel({
    draft,
    isAdding,
    presets,
    filteredPresets,
    selectedPresetId,
    showPresetPicker,
    presetSearch,
    showKey,
    onToggleKey,
    onChange,
    onApplyPreset,
    onOpenPresetPicker,
    onClosePresetPicker,
    onPresetSearch,
    onSubmit,
    onDelete,
    onTest,
}) {
    const selectedPreset = presets.find((preset) => preset.id === selectedPresetId);
    const typeOptions = providerTypeOptionsWithCurrent(presets, draft.type);
    return (
        <form className="space-y-7" onSubmit={onSubmit}>
            {isAdding && presets.length > 0 && (
                <div className="rounded-2xl border border-zinc-800 bg-zinc-950/30 p-4">
                    <div className="flex flex-wrap items-center justify-between gap-3">
                        <div>
                            <h2 className="text-xl font-bold">供应商预设</h2>
                            <p className="mt-1 text-sm text-zinc-500">选择后会填入地址、供应商分类，并自动导入常用模型。</p>
                        </div>
                        <button className="rounded-xl border border-zinc-700 px-4 py-2 font-semibold text-zinc-100 hover:bg-zinc-900" type="button" onClick={onOpenPresetPicker}>
                            {selectedPreset ? `已选：${selectedPreset.name}` : "选择预设"}
                        </button>
                    </div>

                    {showPresetPicker && (
                        <Modal title="选择供应商预设" onClose={onClosePresetPicker}>
                            <div className="space-y-4">
                                <label className="flex items-center gap-2 rounded-xl border border-zinc-700 bg-[#151515] px-3 py-2 text-zinc-500">
                                    <SearchIcon />
                                    <input
                                        className="min-w-0 flex-1 bg-transparent text-sm text-zinc-100 outline-none placeholder:text-zinc-600"
                                        value={presetSearch}
                                        onChange={(event) => onPresetSearch(event.target.value)}
                                        placeholder="搜索名称、类型、地址或模型..."
                                        autoFocus
                                    />
                                </label>

                                <div className="max-h-[60vh] space-y-3 overflow-y-auto pr-1">
                                    {filteredPresets.map((preset) => (
                                        <button
                                            key={preset.id}
                                            className={`w-full rounded-2xl border p-4 text-left transition ${
                                                selectedPresetId === preset.id
                                                    ? "border-emerald-500 bg-emerald-950/30"
                                                    : "border-zinc-800 bg-zinc-950/40 hover:bg-zinc-900"
                                            }`}
                                            type="button"
                                            onClick={() => onApplyPreset(preset)}
                                        >
                                            <div className="flex flex-wrap items-start justify-between gap-3">
                                                <div>
                                                    <div className="text-lg font-bold text-zinc-100">{preset.name}</div>
                                                    <div className="mt-1 text-xs text-zinc-500">{preset.id} · {preset.type}</div>
                                                </div>
                                                <span className="rounded-full bg-zinc-800 px-2.5 py-1 text-xs text-zinc-400">{preset.models?.length || 0} 个模型</span>
                                            </div>
                                            <div className="mt-3 rounded-xl bg-black/20 px-3 py-2 font-mono text-xs text-zinc-400">{preset.baseUrl}</div>
                                            <div className="mt-3 flex flex-wrap gap-2">
                                                {(preset.models || []).slice(0, 5).map((model) => (
                                                    <span key={model.id} className="rounded-full border border-zinc-800 px-2 py-1 text-xs text-zinc-400">{model.name || model.id}</span>
                                                ))}
                                            </div>
                                        </button>
                                    ))}
                                    {!filteredPresets.length && <div className="rounded-2xl border border-dashed border-zinc-800 p-8 text-center text-zinc-500">没有匹配的预设。</div>}
                                </div>
                            </div>
                        </Modal>
                    )}
                </div>
            )}

            {isAdding && (
                <div className="grid gap-4 md:grid-cols-3">
                    <Field label="平台 ID" value={draft.id} onChange={(id) => onChange({id})} placeholder="right-code-gemini" />
                    <Field label="平台名称" value={draft.name} onChange={(name) => onChange({name})} placeholder="Right Code Gemini" />
                    <SelectField label="供应商分类" value={draft.type} onChange={(type) => onChange({type})} options={typeOptions} />
                </div>
            )}

            <div>
                <div className="mb-3 flex items-center justify-between">
                    <h2 className="text-xl font-bold">API 密钥</h2>
                    <span className="text-sm text-zinc-500">本地加密存储</span>
                </div>
                <div className="flex overflow-hidden rounded-xl border border-zinc-700 bg-[#151515]">
                    <input
                        className="min-w-0 flex-1 bg-transparent px-4 py-3 text-lg outline-none placeholder:text-zinc-600"
                        type={showKey ? "text" : "password"}
                        value={draft.apiKey}
                        onChange={(event) => onChange({apiKey: event.target.value})}
                        placeholder="sk-..."
                    />
                    <button className="border-l border-zinc-700 px-4 text-zinc-400 hover:bg-zinc-900" type="button" onClick={onToggleKey} aria-label={showKey ? "隐藏 API 密钥" : "显示 API 密钥"}>
                        {showKey ? <EyeOffIcon /> : <EyeIcon />}
                    </button>
                    <button className="border-l border-zinc-700 px-5 font-bold hover:bg-zinc-900" type="button" onClick={onTest}>检测</button>
                </div>
                <p className="mt-2 text-right text-sm text-zinc-500">多个密钥使用逗号分隔</p>
            </div>

            <div>
                <div className="mb-3 flex items-center justify-between">
                    <h2 className="text-xl font-bold">API 地址</h2>
                    <span className="text-sm text-zinc-500">OpenAI 兼容入口通常以 /v1 结尾</span>
                </div>
                <input
                    className="w-full rounded-xl border border-zinc-700 bg-[#151515] px-4 py-3 text-lg outline-none placeholder:text-zinc-600"
                    value={draft.baseUrl}
                    onChange={(event) => onChange({baseUrl: event.target.value})}
                    placeholder="https://api.openai.com/v1"
                />
                <p className="mt-2 text-sm text-zinc-500">预览：{previewProviderUrl(draft.baseUrl, draft.capabilityConfig)}</p>
            </div>

            <ProviderCapabilityEditor value={draft.capabilityConfig} onChange={(capabilityConfig) => onChange({capabilityConfig})} />

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
        <form className="space-y-5" onSubmit={onSubmit}>
            <div className="grid gap-3 md:grid-cols-2">
                <Field label="模型 ID" value={draft.id} disabled={editing} onChange={(id) => onChange({id})} placeholder="gemini-3-flash-preview" />
                <Field label="显示名称" value={draft.name} onChange={(name) => onChange({name})} placeholder="gemini-3-flash-preview" />
                <Field label="上下文长度" type="number" value={draft.contextLength} onChange={(contextLength) => onChange({contextLength})} />
                <Field label="Max Tokens" type="number" value={draft.maxTokens} onChange={(maxTokens) => onChange({maxTokens})} />
            </div>

            <ModelCapabilityEditor value={draft.capabilities} onChange={(capabilities) => onChange({capabilities})} />

            <label className="flex items-center gap-2 text-sm font-semibold text-zinc-300">
                <input type="checkbox" checked={draft.enabled !== false} onChange={(event) => onChange({enabled: event.target.checked})} />
                对外提供
            </label>

            <div className="flex justify-end gap-2 border-t border-zinc-800 pt-4">
                <button className="rounded-xl border border-zinc-700 px-4 py-2 font-bold text-zinc-300" type="button" onClick={onCancel}>取消</button>
                <button className="rounded-xl bg-zinc-100 px-4 py-2 font-bold text-zinc-950" type="submit">{editing ? "更新" : "添加"}</button>
            </div>
        </form>
    );
}

function ProviderTestForm({provider, models, modelId, result, testing, onModelChange, onRun}) {
    return (
        <div className="space-y-4">
            <div className="rounded-2xl border border-zinc-800 bg-zinc-950/30 p-4 text-sm text-zinc-400">
                <div className="font-semibold text-zinc-200">{provider.name}</div>
                <div className="mt-1 font-mono text-xs">provider id: {provider.id}</div>
                <div className="mt-2">会向所选上游模型发送测试消息：<span className="text-zinc-200">Reply with OK.</span></div>
            </div>

            <SelectField
                label="选择测试模型"
                value={modelId}
                onChange={onModelChange}
                options={models.map((model) => [model.id, `${model.name || model.id} (${provider.id}/${model.id})`])}
            />

            {result && (
                <div className={`rounded-2xl border p-4 ${result.ok ? "border-emerald-800 bg-emerald-950/30" : "border-red-900 bg-red-950/30"}`}>
                    <div className={`font-bold ${result.ok ? "text-emerald-300" : "text-red-300"}`}>
                        {result.ok ? "检测通过" : "检测失败"}
                    </div>
                    {result.ok ? (
                        <div className="mt-2 space-y-1 text-sm text-zinc-300">
                            <div>模型：{result.model}</div>
                            <div>耗时：{result.latencyMs} ms</div>
                            <div>回复：{result.content || "（无文本内容）"}</div>
                        </div>
                    ) : (
                        <pre className="mt-2 whitespace-pre-wrap text-sm text-red-100">{result.error}</pre>
                    )}
                </div>
            )}

            <div className="flex justify-end border-t border-zinc-800 pt-4">
                <button
                    className="rounded-xl bg-zinc-100 px-5 py-2.5 font-bold text-zinc-950 disabled:cursor-wait disabled:opacity-60"
                    type="button"
                    disabled={testing || !modelId}
                    onClick={onRun}
                >
                    {testing ? "检测中…" : "发送测试消息"}
                </button>
            </div>
        </div>
    );
}

function ModelGroup({family, models, onEdit, onDelete}) {
    return (
        <div className="overflow-hidden rounded-2xl border border-zinc-800">
            <div className="flex items-center justify-between bg-zinc-800/70 px-6 py-4">
                <div className="flex items-center gap-4">
                    <span className="text-zinc-500">模型组</span>
                    <h3 className="text-xl font-bold">{family}</h3>
                </div>
                <span className="text-sm text-zinc-400">{models.length} 个</span>
            </div>
            <div className="divide-y divide-zinc-900 bg-[#151515]">
                {models.map((model) => (
                    <div key={`${model.providerId}/${model.id}`} className="flex items-center gap-4 px-6 py-4">
                        <span className="grid h-9 w-9 place-items-center rounded-full bg-zinc-800 text-sm text-zinc-400">AI</span>
                        <div className="min-w-0 flex-1">
                            <div className="truncate text-xl font-semibold">{model.name || model.id}</div>
                            <div className="text-xs text-zinc-500">context {model.contextLength || 0} · max {model.maxTokens || 0} · route {model.providerId}/{model.id}</div>
                        </div>
                        <span className={`rounded-full px-2.5 py-1 text-xs font-bold ${model.enabled !== false ? "bg-green-950 text-green-400" : "bg-zinc-800 text-zinc-500"}`}>
                            {model.enabled !== false ? "OPEN" : "OFF"}
                        </span>
                        <CapabilityPills value={model.capabilities} />
                        <button className="rounded-lg border border-zinc-700 px-3 py-1.5 text-sm text-zinc-300 hover:bg-zinc-900" type="button" onClick={() => onEdit(model)}>编辑</button>
                        <button className="rounded-lg border border-red-900/70 px-3 py-1.5 text-sm text-red-300 hover:bg-red-950/40" type="button" onClick={() => onDelete(model)}>删除</button>
                    </div>
                ))}
            </div>
        </div>
	);
}

function APIKeyPanel({items, visibleKeys, onNew, onEdit, onDelete, onCopy, onToggleVisible}) {
	return (
		<section className="mt-7 rounded-2xl border border-zinc-800 bg-zinc-950/30 p-5">
			<div className="mb-4 flex flex-wrap items-center justify-between gap-3">
				<div>
					<h2 className="text-xl font-bold">API Key 管理</h2>
					<p className="mt-1 text-sm text-zinc-500">只用于应用统计口径；请求不会因为 key 无效被拒绝。</p>
				</div>
				<button className="rounded-xl bg-zinc-100 px-4 py-2.5 font-bold text-zinc-950" type="button" onClick={onNew}>＋ 新增</button>
			</div>
			<div className="overflow-hidden rounded-xl border border-zinc-800">
				<table className="w-full min-w-[760px] text-left text-sm">
					<thead className="bg-zinc-900/70 text-xs text-zinc-500">
						<tr>
							<th className="px-4 py-3">应用名称</th>
							<th className="px-4 py-3">描述</th>
							<th className="px-4 py-3">Key</th>
							<th className="px-4 py-3 text-right">操作</th>
						</tr>
					</thead>
					<tbody className="divide-y divide-zinc-800">
						{items.map((item) => {
							const visible = visibleKeys[item.id];
							return (
								<tr key={item.id}>
									<td className="px-4 py-3 font-semibold text-zinc-100">{item.name}</td>
									<td className="px-4 py-3 text-zinc-400">{item.description || "—"}</td>
									<td className="px-4 py-3 font-mono text-xs text-zinc-300">{visible ? item.key : maskKey(item.key)}</td>
									<td className="px-4 py-3">
										<div className="flex justify-end gap-2">
											<button className="rounded-lg border border-zinc-700 px-3 py-1.5 text-zinc-300 hover:bg-zinc-900" type="button" onClick={() => onCopy(item.key)}>复制</button>
											<button className="rounded-lg border border-zinc-700 px-3 py-1.5 text-zinc-300 hover:bg-zinc-900" type="button" onClick={() => onToggleVisible(item.id)}>{visible ? "隐藏" : "显示"}</button>
											<button className="rounded-lg border border-zinc-700 px-3 py-1.5 text-zinc-300 hover:bg-zinc-900" type="button" onClick={() => onEdit(item)}>编辑</button>
											<button className="rounded-lg border border-red-900/70 px-3 py-1.5 text-red-300 hover:bg-red-950/40" type="button" onClick={() => onDelete(item)}>删除</button>
										</div>
									</td>
								</tr>
							);
						})}
						{!items.length && (
							<tr>
								<td className="px-4 py-8 text-center text-zinc-500" colSpan="4">暂无 API Key。点右上角新增一个应用。</td>
							</tr>
						)}
					</tbody>
				</table>
			</div>
		</section>
	);
}

function APIKeyForm({draft, editing, onChange, onSubmit, onCancel}) {
	return (
		<form className="space-y-4" onSubmit={onSubmit}>
			<Field label="应用名称" value={draft.name} onChange={(name) => onChange({name})} placeholder="例如 Raycast / Cursor" />
			<Field label="描述" value={draft.description || ""} onChange={(description) => onChange({description})} placeholder="可选，用来备注来源" />
			<p className="text-sm text-zinc-500">{editing ? "Key 本身不会被修改。" : "保存后自动生成 sk- 开头的 32 位 key。"}</p>
			<div className="flex justify-end gap-2">
				<button className="rounded-xl border border-zinc-700 px-4 py-2.5 font-semibold text-zinc-300" type="button" onClick={onCancel}>取消</button>
				<button className="rounded-xl bg-zinc-100 px-4 py-2.5 font-bold text-zinc-950" type="submit">保存</button>
			</div>
		</form>
	);
}

function StatsPanel({baseUrl, stats, statRows, trendRows, logPage, logPageNum, table, chart, grain, stackBy, metric, filter, providers, modelOptions, appOptions, onChange, onTable, onChart, onGrain, onStackBy, onMetric, onPage, onRefresh}) {
    const points = stats.points || [];
    const nonCacheInputTokens = nonCacheInput(stats);
    const maxPage = Math.max(1, Math.ceil((logPage.total || 0) / 50));
    return (
        <section className="mt-7 rounded-2xl border border-zinc-800 bg-zinc-950/30 p-5">
            <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
                <div>
                    <h2 className="text-xl font-bold">Token 统计</h2>
                    <p className="mt-1 text-sm text-zinc-500">本地入口：{baseUrl ? `${baseUrl}/v1/chat/completions` : "启动中…"}；上游协议由平台配置决定。</p>
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
                    <SelectField
                        label="模型 ID"
                        value={filter.modelId}
                        onChange={(modelId) => onChange({modelId})}
                        options={[["", modelOptions.length ? "全部" : "暂无记录"], ...modelOptions.map((model) => [model, model])]}
                    />
                    <SelectField
                        label="应用"
                        value={filter.appName}
                        onChange={(appName) => onChange({appName})}
                        options={[["", appOptions.length ? "全部" : "暂无记录"], ...appOptions.map((app) => [app, app])]}
                    />
                    <button className="rounded-xl bg-zinc-100 px-4 py-2.5 font-bold text-zinc-950" type="button" onClick={onRefresh}>刷新</button>
                </div>
            </div>

            <div className="grid gap-3 md:grid-cols-5">
                <StatCard label="调用" value={stats.calls || 0} />
                <StatCard label="输入" value={stats.inputTokens || 0} />
                <StatCard label="输出" value={stats.outputTokens || 0} />
                <StatCard label="未命中输入" value={nonCacheInputTokens} />
                <StatCard label="缓存命中" value={stats.cacheReadInputTokens || 0} />
            </div>

            <div className="mt-4 flex flex-wrap gap-3 text-xs text-zinc-400">
                <Legend color="bg-emerald-500" label="非缓存输入" />
                <Legend color="bg-sky-500" label="输出" />
                <Legend color="bg-amber-500" label="缓存写入" />
                <Legend color="bg-violet-500" label="缓存命中" />
            </div>

            <div className="mt-6 rounded-xl border border-zinc-800 bg-[#151515] p-4">
                <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
                    <div className="flex flex-wrap gap-2">
                        <TabButton active={chart === "appPie"} onClick={() => onChart("appPie")}>应用饼图</TabButton>
                        <TabButton active={chart === "modelPie"} onClick={() => onChart("modelPie")}>模型饼图</TabButton>
                        <TabButton active={chart === "providerPie"} onClick={() => onChart("providerPie")}>平台饼图</TabButton>
                        <TabButton active={chart === "bar"} onClick={() => onChart("bar")}>时间柱图</TabButton>
                        <TabButton active={chart === "line"} onClick={() => onChart("line")}>趋势折线</TabButton>
                    </div>
                    <div className="flex flex-wrap gap-2">
                        {(chart === "bar" || chart === "line") && (
                            <>
                                <SelectField label="粒度" value={grain} onChange={onGrain} options={[["hour", "小时"], ["day", "天"], ["week", "周"]]} />
                                <SelectField label="分组" value={stackBy} onChange={onStackBy} options={[["app", "应用"], ["model", "模型"], ["provider", "平台"]]} />
                                {chart === "line" && <SelectField label="指标" value={metric} onChange={onMetric} options={[["total", "总量"], ["input", "输入"], ["output", "输出"], ["cacheRead", "缓存命中"]]} />}
                            </>
                        )}
                    </div>
                </div>
                <StatsChart chart={chart} statRows={statRows} trendRows={trendRows} points={points} metric={metric} />
            </div>

            <div className="mt-6 rounded-xl border border-zinc-800 bg-[#151515] p-4">
                <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
                    <div className="flex flex-wrap gap-2">
                        <TabButton active={table === "logs"} onClick={() => onTable("logs")}>请求日志</TabButton>
                        <TabButton active={table === "provider"} onClick={() => onTable("provider")}>按平台</TabButton>
                        <TabButton active={table === "model"} onClick={() => onTable("model")}>按模型</TabButton>
                        <TabButton active={table === "app"} onClick={() => onTable("app")}>按应用</TabButton>
                    </div>
                    {table === "logs" && <button className="rounded-xl border border-zinc-700 px-3 py-2 text-sm font-semibold text-zinc-300 hover:bg-zinc-900" type="button" onClick={() => exportFilteredLogs(filter)}>导出 CSV</button>}
                </div>
                {table === "logs" ? (
                    <>
                        <CallLogTable items={logPage.items || []} />
                        <div className="mt-4 flex items-center justify-between text-sm text-zinc-400">
                            <span>共 {logPage.total || 0} 条 · 第 {logPageNum} / {maxPage} 页</span>
                            <div className="flex gap-2">
                                <button className="rounded-lg border border-zinc-700 px-3 py-1.5 disabled:opacity-40" type="button" disabled={logPageNum <= 1} onClick={() => onPage(logPageNum - 1)}>上一页</button>
                                <button className="rounded-lg border border-zinc-700 px-3 py-1.5 disabled:opacity-40" type="button" disabled={logPageNum >= maxPage} onClick={() => onPage(logPageNum + 1)}>下一页</button>
                            </div>
                        </div>
                    </>
                ) : (
                    <StatRowsTable rows={statRows[table] || []} />
                )}
            </div>
        </section>
    );
}

function StatsChart({chart, statRows, trendRows, points, metric}) {
    const option = useMemo(() => {
        if (chart.endsWith("Pie")) {
            const key = chart === "appPie" ? "app" : chart === "modelPie" ? "model" : "provider";
            return pieOption(statRows[key] || []);
        }
        if (chart === "bar") return timeOption(trendRows || [], "bar");
        if (chart === "line") return lineOption(trendRows || [], points || [], metric);
        return {};
    }, [chart, statRows, trendRows, points, metric]);
    return <EChart option={option} empty={!hasChartData(option)} />;
}

function EChart({option, empty}) {
    const ref = useRef(null);
    useEffect(() => {
        if (!ref.current || empty) return undefined;
        const chart = echarts.init(ref.current, "dark");
        chart.setOption(option);
        const resize = () => chart.resize();
        window.addEventListener("resize", resize);
        return () => {
            window.removeEventListener("resize", resize);
            chart.dispose();
        };
    }, [option, empty]);
    if (empty) return <div className="grid h-72 place-items-center text-zinc-500">暂无调用日志。</div>;
    return <div ref={ref} className="h-80 w-full" />;
}

function CallLogTable({items}) {
    return (
        <div className="overflow-x-auto">
            <table className="w-full min-w-[1100px] text-left text-sm">
                <thead className="bg-zinc-900/70 text-xs text-zinc-500">
                    <tr>
                        {["调用时间", "提供商", "模型", "应用", "输入", "输出", "缓存命中", "状态码", "协议", "耗时ms", "流式", "错误信息"].map((label) => <th key={label} className="px-3 py-3">{label}</th>)}
                    </tr>
                </thead>
                <tbody className="divide-y divide-zinc-800">
                    {items.map((item) => (
                        <tr key={item.id}>
                            <td className="px-3 py-3 text-zinc-300">{formatTime(item.startedAt)}</td>
                            <td className="px-3 py-3 text-zinc-400">{item.providerId || "—"}</td>
                            <td className="px-3 py-3 text-zinc-400">{item.modelId || "—"}</td>
                            <td className="px-3 py-3 text-zinc-300">{item.appName || "无应用"}</td>
                            <td className="px-3 py-3">{fmt(item.inputTokens)}</td>
                            <td className="px-3 py-3">{fmt(item.outputTokens)}</td>
                            <td className="px-3 py-3">{fmt(item.cacheReadInputTokens)}</td>
                            <td className="px-3 py-3">{item.statusCode || "—"}</td>
                            <td className="px-3 py-3 text-zinc-400">{item.protocol}</td>
                            <td className="px-3 py-3">{fmt(item.durationMs)}</td>
                            <td className="px-3 py-3">{item.stream ? "是" : "否"}</td>
                            <td className="max-w-[280px] truncate px-3 py-3 text-red-300" title={item.error}>{item.error || "—"}</td>
                        </tr>
                    ))}
                    {!items.length && <tr><td className="px-4 py-8 text-center text-zinc-500" colSpan="12">暂无调用日志。</td></tr>}
                </tbody>
            </table>
        </div>
    );
}

function StatRowsTable({rows}) {
    return (
        <div className="overflow-x-auto">
            <table className="w-full min-w-[780px] text-left text-sm">
                <thead className="bg-zinc-900/70 text-xs text-zinc-500">
                    <tr>
                        {["名称", "调用次数", "输入", "输出", "缓存写入", "缓存命中", "总 Token", "占比"].map((label) => <th key={label} className="px-4 py-3">{label}</th>)}
                    </tr>
                </thead>
                <tbody className="divide-y divide-zinc-800">
                    {rows.map((row) => (
                        <tr key={row.name}>
                            <td className="px-4 py-3 font-semibold text-zinc-100">{row.name}</td>
                            <td className="px-4 py-3">{fmt(row.calls)}</td>
                            <td className="px-4 py-3">{fmt(row.inputTokens)}</td>
                            <td className="px-4 py-3">{fmt(row.outputTokens)}</td>
                            <td className="px-4 py-3">{fmt(row.cacheCreationInputTokens)}</td>
                            <td className="px-4 py-3">{fmt(row.cacheReadInputTokens)}</td>
                            <td className="px-4 py-3">{fmt(row.totalTokens)}</td>
                            <td className="px-4 py-3">{Math.round((row.share || 0) * 1000) / 10}%</td>
                        </tr>
                    ))}
                    {!rows.length && <tr><td className="px-4 py-8 text-center text-zinc-500" colSpan="8">暂无统计。</td></tr>}
                </tbody>
            </table>
        </div>
    );
}

function TabButton({active, onClick, children}) {
    return (
        <button className={`rounded-lg px-3 py-1.5 text-sm font-semibold ${active ? "bg-zinc-100 text-zinc-950" : "border border-zinc-700 text-zinc-300 hover:bg-zinc-900"}`} type="button" onClick={onClick}>
            {children}
        </button>
    );
}

function pieOption(rows) {
    return {
        backgroundColor: "transparent",
        tooltip: {trigger: "item"},
        legend: {bottom: 0, textStyle: {color: "#a1a1aa"}},
        series: [{type: "pie", radius: ["42%", "68%"], data: rows.map((row) => ({name: row.name, value: row.totalTokens || 0}))}],
    };
}

function timeOption(rows, type) {
    const buckets = [...new Set(rows.map((row) => row.bucket))];
    const names = [...new Set(rows.map((row) => row.name))];
    return {
        backgroundColor: "transparent",
        tooltip: {trigger: "axis"},
        legend: {top: 0, textStyle: {color: "#a1a1aa"}},
        xAxis: {type: "category", data: buckets},
        yAxis: {type: "value"},
        series: names.map((name) => ({
            name,
            type,
            stack: type === "bar" ? "tokens" : undefined,
            smooth: type === "line",
            data: buckets.map((bucket) => metricValue(rows.find((row) => row.bucket === bucket && row.name === name) || {}, "total")),
        })),
    };
}

function lineOption(rows, points, metric) {
    if (rows.length) {
        const buckets = [...new Set(rows.map((row) => row.bucket))];
        const names = [...new Set(rows.map((row) => row.name))];
        return {
            backgroundColor: "transparent",
            tooltip: {trigger: "axis"},
            legend: {top: 0, textStyle: {color: "#a1a1aa"}},
            xAxis: {type: "category", data: buckets},
            yAxis: {type: "value"},
            series: names.map((name) => ({
                name,
                type: "line",
                smooth: true,
                data: buckets.map((bucket) => metricValue(rows.find((row) => row.bucket === bucket && row.name === name) || {}, metric)),
            })),
        };
    }
    return {
        backgroundColor: "transparent",
        tooltip: {trigger: "axis"},
        legend: {top: 0, textStyle: {color: "#a1a1aa"}},
        xAxis: {type: "category", data: points.map((point) => point.date)},
        yAxis: {type: "value"},
        series: [
            {name: "输入", type: "line", smooth: true, data: points.map((point) => point.inputTokens || 0)},
            {name: "输出", type: "line", smooth: true, data: points.map((point) => point.outputTokens || 0)},
            {name: "缓存命中", type: "line", smooth: true, data: points.map((point) => point.cacheReadInputTokens || 0)},
        ],
    };
}

function metricValue(row, metric) {
    if (metric === "input") return row.inputTokens || 0;
    if (metric === "output") return row.outputTokens || 0;
    if (metric === "cacheRead") return row.cacheReadInputTokens || 0;
    return tokenTotal(row);
}

function hasChartData(option) {
    return (option.series || []).some((series) => (series.data || []).some((item) => (typeof item === "number" ? item : item.value) > 0));
}

function tokenParts(point) {
    return [
        {name: "input", value: nonCacheInput(point), color: "#10b981"},
        {name: "output", value: point.outputTokens || 0, color: "#0ea5e9"},
        {name: "cacheCreate", value: point.cacheCreationInputTokens || 0, color: "#f59e0b"},
        {name: "cacheRead", value: point.cacheReadInputTokens || 0, color: "#8b5cf6"},
    ].filter((part) => part.value > 0);
}

function nonCacheInput(point) {
    return Math.max(0, (point.inputTokens || 0) - (point.cacheCreationInputTokens || 0) - (point.cacheReadInputTokens || 0));
}

function tokenTotal(point) {
    return (point.inputTokens || 0) + (point.outputTokens || 0);
}

function statsFilterPayload(filter) {
    return {
        from: filter.from ? `${filter.from}T00:00:00Z` : "",
        to: filter.to ? `${filter.to}T23:59:59Z` : "",
        providerId: filter.providerId || "",
        modelId: filter.modelId || "",
        appName: filter.appName || "",
    };
}

function maskKey(key = "") {
    if (key.length <= 12) return key;
    return `${key.slice(0, 7)}…${key.slice(-6)}`;
}

function fmt(value) {
    return Number(value || 0).toLocaleString();
}

function formatTime(value) {
    if (!value) return "—";
    return value.replace("T", " ").replace("Z", "");
}

async function exportFilteredLogs(filter) {
    const page = await CallLogs(statsFilterPayload(filter), 1, 10000);
    if ((page.total || 0) > (page.items || []).length) {
        alert(`当前筛选共 ${page.total} 条日志，本次导出前 ${(page.items || []).length} 条。请缩小时间范围后导出完整结果。`);
    }
    exportLogsCSV(page.items || []);
}

function exportLogsCSV(items) {
    const headers = ["调用时间", "提供商", "模型", "应用", "输入", "输出", "缓存命中", "状态码", "协议", "耗时ms", "是否流式", "错误信息"];
    const rows = items.map((item) => [
        item.startedAt,
        item.providerId,
        item.modelId,
        item.appName || "无应用",
        item.inputTokens,
        item.outputTokens,
        item.cacheReadInputTokens,
        item.statusCode,
        item.protocol,
        item.durationMs,
        item.stream ? "是" : "否",
        item.error || "",
    ]);
    const csv = [headers, ...rows].map((row) => row.map(csvCell).join(",")).join("\n");
    const url = URL.createObjectURL(new Blob([`\uFEFF${csv}`], {type: "text/csv;charset=utf-8"}));
    const link = document.createElement("a");
    link.href = url;
    link.download = `localrelay-call-logs-${today()}.csv`;
    link.click();
    URL.revokeObjectURL(url);
}

function csvCell(value) {
    return `"${String(value ?? "").replaceAll("\"", "\"\"")}"`;
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

function StatusMessage({message, providerCount}) {
    return (
        <div className="mt-6 rounded-xl border border-zinc-800 bg-zinc-950/50 px-4 py-3 text-sm text-zinc-400">
            {message} · 当前平台 {providerCount} 个
        </div>
    );
}

function ProviderCapabilityEditor({value, onChange}) {
    const cfg = parseJsonObject(value, {protocol: "openai_chat"});
    const thinking = cfg.thinking || {};
    const reasoningEffort = cfg.reasoningEffort || {};
    const toolCalls = cfg.toolCalls || {};
    const preview = formatJSON(cleanProviderCapability(cfg));

    function update(patch) {
        onChange(formatJSON(cleanProviderCapability({...cfg, ...patch})));
    }

    function updateThinking(patch) {
        update({thinking: {...thinking, ...patch}});
    }

    function updateReasoning(patch) {
        update({reasoningEffort: {...reasoningEffort, ...patch}});
    }

    return (
        <details className="rounded-2xl border border-zinc-800 bg-zinc-950/30">
            <summary className="flex cursor-pointer list-none flex-wrap items-center justify-between gap-3 px-4 py-4">
                <div>
                    <h2 className="text-xl font-bold">提供商属性配置</h2>
                    <p className="mt-1 text-sm text-zinc-500">默认折叠。展开后用选项描述协议差异。</p>
                </div>
                <span className="rounded-full bg-zinc-800 px-2.5 py-1 text-xs text-zinc-400">{cfg.protocol || "openai_chat"}</span>
            </summary>

            <div className="space-y-4 border-t border-zinc-800 p-4">
                <div className="grid gap-4 md:grid-cols-2">
                    <SelectField
                        label="上游协议"
                        value={cfg.protocol || "openai_chat"}
                        onChange={(protocol) => update({protocol})}
                        options={providerProtocolOptions}
                    />
                    <SelectField
                        label="推理强度字段"
                        value={reasoningEffort.field || ""}
                        onChange={(field) => updateReasoning({field})}
                        options={[["", "不转发"], ["reasoning_effort", "reasoning_effort"]]}
                    />
                    <SelectField
                        label="历史思考字段"
                        value={thinking.requestMessageField || ""}
                        onChange={(requestMessageField) => updateThinking({requestMessageField})}
                        options={[["", "不映射"], ["reasoning_content", "reasoning_content"]]}
                    />
                    <SelectField
                        label="响应思考字段"
                        value={thinking.responseContentField || ""}
                        onChange={(responseContentField) => updateThinking({responseContentField})}
                        options={[["", "不映射"], ["reasoning_content", "reasoning_content"]]}
                    />
                </div>

                <MultiCheckField
                    label="思考请求字段"
                    values={thinking.requestFields || []}
                    options={thinkingRequestFieldOptions}
                    onChange={(requestFields) => updateThinking({requestFields})}
                />

                <MultiCheckField
                    label="允许的推理强度"
                    values={reasoningEffort.values || []}
                    options={reasoningValueOptions}
                    onChange={(values) => updateReasoning({values})}
                />

                <ReasoningMapField
                    valueMap={reasoningEffort.valueMap || {}}
                    onChange={(valueMap) => updateReasoning({valueMap})}
                />

                <label className="flex items-center gap-2 text-sm font-semibold text-zinc-300">
                    <input
                        type="checkbox"
                        checked={toolCalls.requireAssistantContent === true}
                        onChange={(event) => update({toolCalls: {requireAssistantContent: event.target.checked}})}
                    />
                    工具调用时补齐 assistant content
                </label>

                <details className="rounded-xl border border-zinc-800 bg-black/20">
                    <summary className="cursor-pointer px-4 py-3 text-sm font-semibold text-zinc-300">查看最终 JSON 配置</summary>
                    <JsonPreview value={preview} />
                </details>
            </div>
        </details>
    );
}

function ModelCapabilityEditor({value, onChange}) {
    const cfg = parseJsonObject(value, {});
    const preview = formatJSON(cleanObject(cfg));

    function toggle(key, checked) {
        onChange(formatJSON(cleanObject({...cfg, [key]: checked})));
    }

    return (
        <section className="rounded-2xl border border-zinc-800 bg-zinc-950/30 p-4">
            <h3 className="text-base font-bold">模型能力配置</h3>
            <div className="mt-3 grid gap-3 sm:grid-cols-2">
                <CheckField label="支持工具调用" checked={cfg.tools === true} onChange={(checked) => toggle("tools", checked)} />
                <CheckField label="支持流式输出" checked={cfg.stream === true} onChange={(checked) => toggle("stream", checked)} />
                <CheckField label="支持视觉输入" checked={cfg.vision === true || cfg.image === true} onChange={(checked) => toggle("vision", checked)} />
                <CheckField label="支持思考模式" checked={cfg.thinking === true} onChange={(checked) => toggle("thinking", checked)} />
            </div>
            <details className="mt-4 rounded-xl border border-zinc-800 bg-black/20">
                <summary className="cursor-pointer px-4 py-3 text-sm font-semibold text-zinc-300">查看最终 JSON 配置</summary>
                <JsonPreview value={preview} />
            </details>
        </section>
    );
}

function JsonPreview({value}) {
    return (
        <pre className="overflow-auto px-4 pb-4 font-mono text-xs leading-6 text-zinc-300">
            {value.split("\n").map((line, index) => {
                const [key, rest] = line.split(/:(.*)/s);
                const isKey = key.includes("\"") && rest !== undefined;
                return (
                    <span key={`${index}-${line}`} className="block">
                        {isKey ? <><span className="text-sky-300">{key}</span><span className="text-zinc-500">:</span><span className="text-emerald-300">{rest}</span></> : line}
                    </span>
                );
            })}
        </pre>
    );
}

function Modal({title, children, onClose}) {
    return (
        <div className="fixed inset-0 z-50 grid place-items-center bg-black/70 p-4" role="dialog" aria-modal="true">
            <div className="max-h-[90vh] w-full max-w-3xl overflow-y-auto rounded-3xl border border-zinc-800 bg-[#151515] p-5 shadow-2xl">
                <div className="mb-4 flex items-center justify-between gap-3 border-b border-zinc-800 pb-4">
                    <h2 className="text-xl font-bold">{title}</h2>
                    <button className="rounded-xl border border-zinc-700 px-3 py-1.5 text-sm text-zinc-300 hover:bg-zinc-900" type="button" onClick={onClose}>关闭</button>
                </div>
                {children}
            </div>
        </div>
    );
}

function Toast({children}) {
    return (
        <div className="fixed bottom-6 right-6 z-50 rounded-2xl border border-emerald-700/60 bg-emerald-950 px-5 py-3 font-semibold text-emerald-100 shadow-2xl">
            {children}
        </div>
    );
}

function NavButton({active, onClick, children}) {
    return (
        <button
            className={`rounded-xl px-3 py-2 text-sm font-semibold transition ${active ? "bg-zinc-100 text-zinc-950" : "border border-zinc-800 text-zinc-300 hover:bg-zinc-900"}`}
            type="button"
            onClick={onClick}
        >
            {children}
        </button>
    );
}

function CapabilityPills({value}) {
    const cfg = parseJsonObject(value, {});
    const pills = [
        ["启用", true, "bg-emerald-950 text-emerald-400"],
        ["视觉", cfg.vision === true || cfg.image === true, "bg-blue-950 text-blue-400"],
        ["流式", cfg.stream === true, "bg-indigo-950 text-indigo-300"],
        ["工具", cfg.tools === true || cfg.functions === true, "bg-orange-950 text-orange-400"],
        ["思考", cfg.thinking === true, "bg-violet-950 text-violet-300"],
    ];
    return (
        <div className="hidden gap-2 md:flex">
            {pills.filter(([, show]) => show).map(([label,, color]) => (
                <span key={label} className={`rounded-full px-3 py-1 text-xs ${color}`}>{label}</span>
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

function SelectField({label, value, onChange, options}) {
    return (
        <label className="grid gap-1 text-sm font-semibold text-zinc-300">
            {label}
            <select
                className="rounded-xl border border-zinc-700 bg-[#151515] px-3 py-2.5 text-zinc-100 outline-none"
                value={value}
                onChange={(event) => onChange(event.target.value)}
            >
                {options.map(([optionValue, label]) => <option key={optionValue} value={optionValue}>{label}</option>)}
            </select>
        </label>
    );
}

function CheckField({label, checked, onChange}) {
    return (
        <label className="flex items-center gap-2 rounded-xl border border-zinc-800 bg-[#151515] px-3 py-2.5 text-sm font-semibold text-zinc-300">
            <input type="checkbox" checked={checked} onChange={(event) => onChange(event.target.checked)} />
            {label}
        </label>
    );
}

function MultiCheckField({label, values, options, onChange}) {
    function toggle(option, checked) {
        onChange(checked ? [...values, option] : values.filter((value) => value !== option));
    }

    return (
        <div className="grid gap-2 text-sm font-semibold text-zinc-300">
            {label}
            <div className="flex flex-wrap gap-2">
                {options.map((option) => (
                    <label key={option} className="flex items-center gap-2 rounded-xl border border-zinc-800 bg-[#151515] px-3 py-2 text-xs text-zinc-300">
                        <input type="checkbox" checked={values.includes(option)} onChange={(event) => toggle(option, event.target.checked)} />
                        {option}
                    </label>
                ))}
            </div>
        </div>
    );
}

function ReasoningMapField({valueMap, onChange}) {
    function update(from, to) {
        onChange(cleanObject({...valueMap, [from]: to}));
    }

    return (
        <div className="grid gap-2 text-sm font-semibold text-zinc-300">
            推理强度映射
            <div className="grid gap-2 md:grid-cols-3">
                {reasoningMapSources.map((from) => (
                    <SelectField
                        key={from}
                        label={`${from} 映射为`}
                        value={valueMap[from] || ""}
                        onChange={(to) => update(from, to)}
                        options={[["", "不映射"], ...reasoningValueOptions.map((value) => [value, value])]}
                    />
                ))}
            </div>
        </div>
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

function providerTypeOptionsWithCurrent(presets, current) {
    const labels = new Map(providerTypeOptions);
    for (const preset of presets || []) {
        if (preset.type && !labels.has(preset.type)) labels.set(preset.type, preset.type);
    }
    if (current && !labels.has(current)) labels.set(current, current);
    return Array.from(labels);
}

function modelFamily(id = "") {
    const parts = id.split("-").filter(Boolean);
    if (parts.length >= 2) return `${parts[0]}-${parts[1]}`;
    return parts[0] || "models";
}

function previewProviderUrl(baseUrl = "", capabilityConfig = "") {
    const trimmed = baseUrl.trim().replace(/\/+$/, "");
    if (!trimmed) return "填写 API 地址后显示";
    const protocol = parseJsonObject(capabilityConfig, {protocol: "openai_chat"}).protocol || "openai_chat";
    if (protocol === "anthropic_messages") return trimmed.endsWith("/messages") ? trimmed : `${trimmed}/messages`;
    if (protocol === "gemini") return `${trimmed}/models/{model}:generateContent`;
    if (protocol === "openai_responses") return trimmed.endsWith("/responses") ? trimmed : `${trimmed}/responses`;
    return trimmed.endsWith("/chat/completions") ? trimmed : `${trimmed}/chat/completions`;
}

function today() {
    return new Date().toISOString().slice(0, 10);
}

function daysAgo(days) {
    const date = new Date();
    date.setDate(date.getDate() - days);
    return date.toISOString().slice(0, 10);
}

function grainAllowed(filter, grain) {
    if (grain === "hour" || !filter.from || !filter.to) return true;
    const days = (new Date(filter.to) - new Date(filter.from)) / 86400000;
    return grain === "day" ? days >= 1 : days >= 7;
}

function coerceGrain(filter, grain) {
    if (grainAllowed(filter, grain)) return grain;
    return grainAllowed(filter, "day") ? "day" : "hour";
}

function SearchIcon() {
    return (
        <svg className="h-4 w-4 shrink-0" viewBox="0 0 20 20" fill="none" aria-hidden="true">
            <circle cx="9" cy="9" r="5.5" stroke="currentColor" strokeWidth="1.8" />
            <path d="M13 13l3.5 3.5" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
        </svg>
    );
}

function EyeIcon() {
    return (
        <svg className="h-5 w-5" viewBox="0 0 24 24" fill="none" aria-hidden="true">
            <path d="M2.5 12s3.5-6 9.5-6 9.5 6 9.5 6-3.5 6-9.5 6-9.5-6-9.5-6Z" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
            <circle cx="12" cy="12" r="3" stroke="currentColor" strokeWidth="1.8" />
        </svg>
    );
}

function EyeOffIcon() {
    return (
        <svg className="h-5 w-5" viewBox="0 0 24 24" fill="none" aria-hidden="true">
            <path d="M3 3l18 18" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
            <path d="M10.6 5.2A9.8 9.8 0 0 1 12 5c6 0 9.5 7 9.5 7a16.8 16.8 0 0 1-3 3.7M6.7 6.8C4 8.5 2.5 12 2.5 12s3.5 7 9.5 7c1.4 0 2.7-.4 3.8-1" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
            <path d="M9.9 9.9A3 3 0 0 0 14.1 14.1" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
        </svg>
    );
}

function parseJsonObject(value, fallback) {
    try {
        const parsed = JSON.parse(value || "{}");
        return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed : fallback;
    } catch {
        return fallback;
    }
}

function formatJSON(value) {
    return JSON.stringify(value, null, 2);
}

function cleanProviderCapability(cfg) {
    return cleanObject({
        ...cfg,
        protocol: cfg.protocol || "openai_chat",
        thinking: cleanObject(cfg.thinking || {}),
        reasoningEffort: cleanObject({
            ...(cfg.reasoningEffort || {}),
            valueMap: cleanObject(cfg.reasoningEffort?.valueMap || {}),
        }),
        toolCalls: cleanObject(cfg.toolCalls || {}),
    });
}

function cleanObject(value) {
    return Object.fromEntries(Object.entries(value || {}).filter(([, item]) => {
        if (item === "" || item === undefined || item === null || item === false) return false;
        if (Array.isArray(item)) return item.length > 0;
        if (typeof item === "object") return Object.keys(item).length > 0;
        return true;
    }));
}

export default App
