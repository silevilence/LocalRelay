import {useEffect, useMemo, useState} from 'react';
import {
    CreateModel,
    CreateProvider,
    DeleteModel,
    DeleteProvider,
    ListModels,
    ListProviders,
    UpdateModel,
    UpdateProvider,
} from "../wailsjs/go/main/App";

const emptyProvider = {id: "", name: "", type: "openai", baseUrl: "", apiKey: ""};
const emptyModel = {id: "", providerId: "", name: "", capabilities: "", contextLength: 0, maxTokens: 0};

function App() {
    const [providers, setProviders] = useState([]);
    const [models, setModels] = useState([]);
    const [selectedProviderId, setSelectedProviderId] = useState("");
    const [providerForm, setProviderForm] = useState(emptyProvider);
    const [modelForm, setModelForm] = useState(emptyModel);
    const [editingProvider, setEditingProvider] = useState(false);
    const [editingModel, setEditingModel] = useState(false);
    const [message, setMessage] = useState("正在加载本地配置…");

    const selectedProvider = useMemo(
        () => providers.find((provider) => provider.id === selectedProviderId),
        [providers, selectedProviderId],
    );

    useEffect(() => {
        refreshProviders();
    }, []);

    useEffect(() => {
        refreshModels(selectedProviderId);
        setModelForm((form) => ({...emptyModel, providerId: selectedProviderId || form.providerId}));
        setEditingModel(false);
    }, [selectedProviderId]);

    async function refreshProviders() {
        try {
            const items = await ListProviders();
            setProviders(items || []);
            setSelectedProviderId((current) => items?.some((item) => item.id === current) ? current : items?.[0]?.id || "");
            setMessage(items?.length ? "配置已加载。" : "先新增一个 Provider。");
        } catch (error) {
            setMessage(`加载 Provider 失败：${error}`);
        }
    }

    async function refreshModels(providerId = selectedProviderId) {
        try {
            setModels((await ListModels(providerId || "")) || []);
        } catch (error) {
            setMessage(`加载 Model 失败：${error}`);
        }
    }

    async function saveProvider(event) {
        event.preventDefault();
        if (!providerForm.id || !providerForm.name || !providerForm.type || !providerForm.baseUrl) {
            setMessage("Provider 的 ID、名称、类型和 Base URL 必填。");
            return;
        }
        try {
            editingProvider ? await UpdateProvider(providerForm) : await CreateProvider(providerForm);
            setMessage(editingProvider ? "Provider 已更新。" : "Provider 已新增。");
            setProviderForm(emptyProvider);
            setEditingProvider(false);
            await refreshProviders();
            setSelectedProviderId(providerForm.id);
        } catch (error) {
            setMessage(`保存 Provider 失败：${error}`);
        }
    }

    async function removeProvider(id) {
        if (!confirm(`删除 Provider「${id}」？关联 Model 会一起删除。`)) return;
        try {
            await DeleteProvider(id);
            setMessage("Provider 已删除。");
            if (selectedProviderId === id) setSelectedProviderId("");
            await refreshProviders();
            await refreshModels("");
        } catch (error) {
            setMessage(`删除 Provider 失败：${error}`);
        }
    }

    async function saveModel(event) {
        event.preventDefault();
        const payload = {
            ...modelForm,
            contextLength: Number(modelForm.contextLength) || 0,
            maxTokens: Number(modelForm.maxTokens) || 0,
        };
        if (!payload.providerId || !payload.id || !payload.name) {
            setMessage("Model 的所属 Provider、ID 和名称必填。");
            return;
        }
        try {
            editingModel ? await UpdateModel(payload) : await CreateModel(payload);
            setMessage(editingModel ? "Model 已更新。" : "Model 已新增。");
            setModelForm({...emptyModel, providerId: payload.providerId});
            setEditingModel(false);
            await refreshModels(selectedProviderId);
        } catch (error) {
            setMessage(`保存 Model 失败：${error}`);
        }
    }

    async function removeModel(model) {
        if (!confirm(`删除 Model「${model.providerId}/${model.id}」？`)) return;
        try {
            await DeleteModel(model.providerId, model.id);
            setMessage("Model 已删除。");
            await refreshModels(selectedProviderId);
        } catch (error) {
            setMessage(`删除 Model 失败：${error}`);
        }
    }

    function editProvider(provider) {
        setProviderForm({
            id: provider.id,
            name: provider.name,
            type: provider.type,
            baseUrl: provider.baseUrl,
            apiKey: provider.apiKey || "",
        });
        setEditingProvider(true);
        setSelectedProviderId(provider.id);
    }

    function editModel(model) {
        setModelForm(model);
        setEditingModel(true);
    }

    return (
        <div className="min-h-screen bg-slate-950 text-slate-100">
            <main className="mx-auto max-w-7xl px-6 py-8">
                <header className="mb-8 flex flex-col gap-3 md:flex-row md:items-end md:justify-between">
                    <div>
                        <p className="text-sm font-semibold uppercase tracking-[0.3em] text-cyan-300">LocalRelay</p>
                        <h1 className="mt-2 text-4xl font-bold tracking-tight">Provider / Model 管理</h1>
                        <p className="mt-3 text-slate-300">本地 SQLite 已接入，API Key 加密存储。</p>
                    </div>
                    <div className="rounded-xl border border-slate-800 bg-slate-900 px-4 py-3 text-sm text-slate-300">
                        {message}
                    </div>
                </header>

                <div className="grid gap-6 lg:grid-cols-[1fr_1.2fr]">
                    <section className="rounded-2xl border border-slate-800 bg-slate-900/70 p-5">
                        <SectionTitle title="Providers" action={`${providers.length} 个`} />
                        <form className="mt-5 grid gap-3" onSubmit={saveProvider}>
                            <Input label="ID" value={providerForm.id} disabled={editingProvider} onChange={(id) => setProviderForm({...providerForm, id})} placeholder="openai" />
                            <Input label="名称" value={providerForm.name} onChange={(name) => setProviderForm({...providerForm, name})} placeholder="OpenAI" />
                            <Input label="类型" value={providerForm.type} onChange={(type) => setProviderForm({...providerForm, type})} placeholder="openai / anthropic / google" />
                            <Input label="Base URL" value={providerForm.baseUrl} onChange={(baseUrl) => setProviderForm({...providerForm, baseUrl})} placeholder="https://api.openai.com/v1" />
                            <Input label="API Key" type="password" value={providerForm.apiKey} onChange={(apiKey) => setProviderForm({...providerForm, apiKey})} placeholder="仅加密落库" />
                            <div className="flex gap-2">
                                <Button>{editingProvider ? "更新 Provider" : "新增 Provider"}</Button>
                                {editingProvider && <Button type="button" muted onClick={() => { setProviderForm(emptyProvider); setEditingProvider(false); }}>取消</Button>}
                            </div>
                        </form>

                        <div className="mt-6 space-y-3">
                            {providers.map((provider) => (
                                <Card key={provider.id} active={provider.id === selectedProviderId} onClick={() => setSelectedProviderId(provider.id)}>
                                    <div className="min-w-0">
                                        <div className="font-semibold text-slate-100">{provider.name}</div>
                                        <div className="truncate text-sm text-slate-400">{provider.id} · {provider.type}</div>
                                        <div className="truncate text-xs text-slate-500">{provider.baseUrl}</div>
                                    </div>
                                    <RowActions onEdit={() => editProvider(provider)} onDelete={() => removeProvider(provider.id)} />
                                </Card>
                            ))}
                        </div>
                    </section>

                    <section className="rounded-2xl border border-slate-800 bg-slate-900/70 p-5">
                        <SectionTitle title="Models" action={selectedProvider ? `当前：${selectedProvider.name}` : "全部"} />
                        <form className="mt-5 grid gap-3 md:grid-cols-2" onSubmit={saveModel}>
                            <Select label="所属 Provider" value={modelForm.providerId} onChange={(providerId) => setModelForm({...modelForm, providerId})} disabled={editingModel}>
                                <option value="">选择 Provider</option>
                                {providers.map((provider) => <option key={provider.id} value={provider.id}>{provider.name}</option>)}
                            </Select>
                            <Input label="ID" value={modelForm.id} disabled={editingModel} onChange={(id) => setModelForm({...modelForm, id})} placeholder="gpt-4.1-mini" />
                            <Input label="名称" value={modelForm.name} onChange={(name) => setModelForm({...modelForm, name})} placeholder="GPT 4.1 mini" />
                            <Input label="能力字段" value={modelForm.capabilities} onChange={(capabilities) => setModelForm({...modelForm, capabilities})} placeholder='{"stream":true,"tools":true}' />
                            <Input label="上下文长度" type="number" value={modelForm.contextLength} onChange={(contextLength) => setModelForm({...modelForm, contextLength})} />
                            <Input label="Max Tokens" type="number" value={modelForm.maxTokens} onChange={(maxTokens) => setModelForm({...modelForm, maxTokens})} />
                            <div className="flex gap-2 md:col-span-2">
                                <Button>{editingModel ? "更新 Model" : "新增 Model"}</Button>
                                {editingModel && <Button type="button" muted onClick={() => { setModelForm({...emptyModel, providerId: selectedProviderId}); setEditingModel(false); }}>取消</Button>}
                            </div>
                        </form>

                        <div className="mt-6 grid gap-3">
                            {models.map((model) => (
                                <Card key={`${model.providerId}/${model.id}`}>
                                    <div className="min-w-0">
                                        <div className="font-semibold text-slate-100">{model.name}</div>
                                        <div className="truncate text-sm text-slate-400">{model.providerId}/{model.id}</div>
                                        <div className="text-xs text-slate-500">context {model.contextLength || 0} · max {model.maxTokens || 0}</div>
                                    </div>
                                    <RowActions onEdit={() => editModel(model)} onDelete={() => removeModel(model)} />
                                </Card>
                            ))}
                            {!models.length && <div className="rounded-xl border border-dashed border-slate-700 p-8 text-center text-slate-500">还没有 Model。</div>}
                        </div>
                    </section>
                </div>
            </main>
        </div>
    )
}

function SectionTitle({title, action}) {
    return (
        <div className="flex items-center justify-between">
            <h2 className="text-xl font-bold">{title}</h2>
            <span className="rounded-full bg-slate-800 px-3 py-1 text-sm text-slate-300">{action}</span>
        </div>
    );
}

function Input({label, value, onChange, type = "text", disabled = false, placeholder = ""}) {
    return (
        <label className="grid gap-1 text-sm text-slate-300">
            {label}
            <input
                className="rounded-xl border border-slate-700 bg-slate-950 px-3 py-2 text-slate-100 outline-none ring-cyan-400/30 placeholder:text-slate-600 focus:ring-4 disabled:cursor-not-allowed disabled:opacity-60"
                type={type}
                value={value}
                disabled={disabled}
                placeholder={placeholder}
                onChange={(event) => onChange(event.target.value)}
            />
        </label>
    );
}

function Select({label, value, onChange, disabled, children}) {
    return (
        <label className="grid gap-1 text-sm text-slate-300">
            {label}
            <select
                className="rounded-xl border border-slate-700 bg-slate-950 px-3 py-2 text-slate-100 outline-none ring-cyan-400/30 focus:ring-4 disabled:cursor-not-allowed disabled:opacity-60"
                value={value}
                disabled={disabled}
                onChange={(event) => onChange(event.target.value)}
            >
                {children}
            </select>
        </label>
    );
}

function Button({children, muted = false, ...props}) {
    return (
        <button
            className={muted
                ? "rounded-xl border border-slate-700 px-4 py-2 font-semibold text-slate-300 transition hover:bg-slate-800"
                : "rounded-xl bg-cyan-400 px-4 py-2 font-semibold text-slate-950 transition hover:bg-cyan-300"}
            {...props}
        >
            {children}
        </button>
    );
}

function Card({children, active = false, onClick}) {
    return (
        <div
            className={`flex items-start justify-between gap-3 rounded-xl border p-4 ${active ? "border-cyan-400 bg-cyan-400/10" : "border-slate-800 bg-slate-950/70"}`}
            onClick={onClick}
        >
            {children}
        </div>
    );
}

function RowActions({onEdit, onDelete}) {
    return (
        <div className="flex shrink-0 gap-2">
            <button className="text-sm font-semibold text-cyan-300 hover:text-cyan-200" type="button" onClick={(event) => { event.stopPropagation(); onEdit(); }}>编辑</button>
            <button className="text-sm font-semibold text-rose-300 hover:text-rose-200" type="button" onClick={(event) => { event.stopPropagation(); onDelete(); }}>删除</button>
        </div>
    );
}

export default App
