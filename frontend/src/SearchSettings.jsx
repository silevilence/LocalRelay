import {useEffect, useState} from 'react';
import {SearchSettings as loadSearchSettings, SaveSearchSettings} from '../wailsjs/go/main/App';

const control = 'mt-2 w-full rounded-xl border border-zinc-700 bg-zinc-950 px-3 py-2.5 text-sm text-zinc-100 focus:border-emerald-500 focus:outline-none';

export default function SearchSettings() {
    const [state, setState] = useState(null);
    const [provider, setProvider] = useState('');
    const [apiKey, setAPIKey] = useState('');
    const [clearKey, setClearKey] = useState(false);
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState('');
    const [message, setMessage] = useState('');
    async function load() {
        try {
            const next = await loadSearchSettings();
            setState(next); setProvider(next.provider); setError('');
            return true;
        } catch (err) { setError(String(err)); return false; }
    }
    useEffect(() => { load(); }, []);
    async function save(event) {
        event.preventDefault(); setBusy(true); setError(''); setMessage('');
        try {
            await SaveSearchSettings({provider, apiKey: clearKey ? '' : apiKey.trim() || null});
            setAPIKey(''); setClearKey(false);
            if (await load()) setMessage('搜索设置已保存，新请求立即生效。');
        } catch (err) { setError(String(err)); }
        finally { setBusy(false); }
    }
    return <div className="mt-7 border-t border-zinc-800 pt-6">
        <div className="flex flex-wrap items-center justify-between gap-3">
            <h3 className="text-lg font-bold">网页搜索</h3>
            {state && <span className={`rounded-full border px-3 py-1 text-xs ${state.hasApiKey ? 'border-emerald-800 text-emerald-300' : 'border-zinc-700 text-zinc-400'}`}>{state.hasApiKey ? 'API Key 已配置' : '待配置 API Key'}</span>}
        </div>
        <p className="mt-2 text-sm leading-6 text-zinc-500">为内网 Agent 提供搜索中转。与资源代理共用端口和开关，搜索配置独立保存。</p>
        {state && <form className="mt-4" onSubmit={save}>
            <fieldset disabled={busy} className="grid gap-4 sm:grid-cols-2 disabled:opacity-60">
                <label className="text-sm text-zinc-300">搜索服务<select className={control} value={provider} onChange={e => { setProvider(e.target.value); setAPIKey(''); setClearKey(false); }}>{state.providers.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
                <label className="text-sm text-zinc-300">API Key<input className={control} type="password" autoComplete="new-password" maxLength={4096} disabled={clearKey} value={apiKey} placeholder={state.hasApiKey && provider === state.provider ? '已保存，留空保留原 Key' : '输入搜索服务 API Key'} onChange={e => setAPIKey(e.target.value)} /></label>
                {state.hasApiKey && <label className="flex items-center gap-2 text-sm text-zinc-400 sm:col-span-2"><input type="checkbox" className="accent-emerald-500" checked={clearKey} onChange={e => setClearKey(e.target.checked)} />清除已保存的 API Key（搜索将不可用）</label>}
            </fieldset>
            <p className="mt-3 text-xs leading-6 text-zinc-500">Key 加密保存在本机，不向内网插件返回。当前支持 Tavily，搜索会消耗其账户额度。资源代理页面可复制搜索接口及插件开发文档地址。</p>
            <button disabled={busy} className="mt-4 rounded-xl bg-zinc-100 px-5 py-2.5 font-bold text-zinc-950 hover:bg-white disabled:opacity-60" type="submit">{busy ? '正在保存…' : '保存搜索设置'}</button>
        </form>}
        {error && <p role="alert" className="mt-4 text-sm text-amber-200">{error}</p>}
        {!state && error && <button type="button" onClick={load} className="mt-3 text-sm text-emerald-300">重新读取搜索设置</button>}
        {message && <p role="status" className="mt-4 text-sm text-zinc-300">{message}</p>}
    </div>;
}
