import {useEffect, useState} from 'react';
import {SearchSettings as loadSearchSettings, SaveSearchSettings} from '../wailsjs/go/main/App';

const control = 'mt-2 w-full rounded-xl border border-zinc-700 bg-zinc-950 px-3 py-2.5 text-sm text-zinc-100 focus:border-emerald-500 focus:outline-none';
const operations = [
    {id: 'searchProvider', label: '搜索服务', tool: 'search', description: '查找网页，返回标题、链接与摘要。'},
    {id: 'fetchProvider', label: '网页抓取服务', tool: 'fetch', description: '读取指定网页，返回 Markdown 正文。'},
];

export default function SearchSettings() {
    const [state, setState] = useState(null);
    const [selection, setSelection] = useState({});
    const [keys, setKeys] = useState({});
    const [clearKeys, setClearKeys] = useState({});
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState('');
    const [message, setMessage] = useState('');
    async function load() {
        try {
            const next = await loadSearchSettings();
            setState(next);
            setSelection({searchProvider: next.searchProvider, fetchProvider: next.fetchProvider});
            setError('');
            return true;
        } catch (err) { setError(String(err)); return false; }
    }
    useEffect(() => { load(); }, []);
    async function save(event) {
        event.preventDefault(); setBusy(true); setError(''); setMessage('');
        try {
            const apiKeys = Object.fromEntries(state.providers.filter(p => clearKeys[p.id] || keys[p.id]?.trim()).map(p => [p.id, clearKeys[p.id] ? '' : keys[p.id].trim()]));
            await SaveSearchSettings({...selection, apiKeys});
            setKeys({}); setClearKeys({});
            if (await load()) setMessage('网页服务设置已保存，新请求立即生效。');
        } catch (err) { setError(String(err)); }
        finally { setBusy(false); }
    }
    return <div className="mt-7 border-t border-zinc-800 pt-6">
        <h3 className="text-lg font-bold">网页搜索与抓取</h3>
        <p className="mt-2 text-sm leading-6 text-zinc-500">为 Agent 提供 search 与 fetch 工具。分别选择服务，同一服务商共用一份 API Key。</p>
        {state && <form className="mt-5" onSubmit={save}>
            <fieldset disabled={busy} className="space-y-5 disabled:opacity-60">
                <legend className="sr-only">网页服务设置</legend>
                <div className="grid gap-4 sm:grid-cols-2">
                    {operations.map(operation => <label key={operation.id} className="rounded-xl border border-zinc-800 bg-zinc-900/40 p-4 text-sm text-zinc-300">
                        <span className="flex items-center justify-between gap-3"><span className="font-semibold">{operation.label}</span><code className="text-xs text-emerald-400">{operation.tool}</code></span>
                        <select className={control} value={selection[operation.id]} onChange={e => setSelection(current => ({...current, [operation.id]: e.target.value}))}>
                            {state.providers.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
                        </select>
                        <span className="mt-2 block text-xs leading-5 text-zinc-500">{operation.description}</span>
                        {!state.hasApiKeys[selection[operation.id]] && !keys[selection[operation.id]]?.trim() && <span className="mt-2 block text-xs text-amber-300">请在下方配置此服务商的 API Key。</span>}
                    </label>)}
                </div>
                {selection.searchProvider && !state.providers.find(p => p.id === selection.searchProvider)?.advancedSearch &&
                    <p className="text-xs leading-6 text-zinc-400">所选搜索服务仅支持 basic 深度；advanced 参数会被忽略并返回警告。查询含过滤条件最多 {state.providers.find(p => p.id === selection.searchProvider)?.maxQueryCharacters} 个字符，超长仍会返回错误。</p>}
                <div className="border-t border-zinc-800 pt-5">
                    <p className="text-sm font-semibold text-zinc-200">服务商凭据</p>
                    <p className="mt-1 text-xs leading-6 text-zinc-500">切换服务不会删除已保存的 Key。留空保留原 Key，勾选清除后保存才会删除。</p>
                    <div className="mt-3 grid gap-4 sm:grid-cols-2">
                        {state.providers.map(p => <div key={p.id} className="rounded-xl border border-zinc-800 p-4">
                            <div className="flex items-center justify-between gap-2"><span className="text-sm font-semibold">{p.name}</span><span className={`text-xs ${state.hasApiKeys[p.id] ? 'text-emerald-400' : 'text-zinc-500'}`}>{state.hasApiKeys[p.id] ? '已配置' : '未配置'}</span></div>
                            <label className="mt-3 block text-xs text-zinc-400">{p.name} API Key
                                <input className={control} type="password" autoComplete="new-password" maxLength={4096} disabled={!!clearKeys[p.id]} value={keys[p.id] || ''} placeholder={state.hasApiKeys[p.id] ? '留空保留已保存的 Key' : '输入 API Key'} onChange={e => setKeys(current => ({...current, [p.id]: e.target.value}))} />
                            </label>
                            {state.hasApiKeys[p.id] && <label className="mt-3 flex items-center gap-2 text-xs text-zinc-400"><input type="checkbox" className="accent-emerald-500" checked={!!clearKeys[p.id]} onChange={e => setClearKeys(current => ({...current, [p.id]: e.target.checked}))} />清除 {p.name} Key</label>}
                            {clearKeys[p.id] && <p className="mt-2 text-xs leading-5 text-amber-300">保存后，所有选择 {p.name} 的网页工具将不可用。</p>}
                        </div>)}
                    </div>
                </div>
            </fieldset>
            <p className="mt-4 text-xs leading-6 text-zinc-500">Key 加密保存在本机，不返回给客户端。搜索与抓取会消耗服务商额度，与资源代理共用端口、开关和出站配置。</p>
            <button disabled={busy} className="mt-4 rounded-xl bg-zinc-100 px-5 py-2.5 font-bold text-zinc-950 hover:bg-white disabled:opacity-60" type="submit">{busy ? '正在保存…' : '保存网页服务设置'}</button>
        </form>}
        {error && <p role="alert" className="mt-4 text-sm text-amber-200">{error}</p>}
        {!state && error && <button type="button" onClick={load} className="mt-3 text-sm text-emerald-300">重新读取设置</button>}
        {message && <p role="status" className="mt-4 text-sm text-zinc-300">{message}</p>}
    </div>;
}
