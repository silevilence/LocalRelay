import {useCallback, useEffect, useState} from 'react';
import {GitHubProxyStatus, SaveGitHubProxyConfig, SetGitHubProxyEnabled} from '../wailsjs/go/main/App';
import AccessAddresses from './AccessAddresses';

const modes = {auto: '自动选择', direct: '仅直连', proxy: '仅代理'};
const control = 'mt-2 w-full rounded-xl border border-zinc-700 bg-zinc-950 px-3 py-2.5 text-sm text-zinc-100 focus:border-emerald-500 focus:outline-none';

function useProxy() {
    const [state, setState] = useState(null);
    const [error, setError] = useState('');
    const [busy, setBusy] = useState(false);
    const [message, setMessage] = useState('');
    const refresh = useCallback(async () => {
        try { const value = await GitHubProxyStatus(); setState(value); setError(''); }
        catch (err) { setError(String(err)); }
    }, []);
    useEffect(() => { refresh(); const timer = setInterval(refresh, 5000); return () => clearInterval(timer); }, [refresh]);
    async function run(action, success) {
        setBusy(true); setMessage('');
        try { await action(); await refresh(); setMessage(success); }
        catch (err) { setMessage(`操作失败：${err}`); await refresh(); }
        finally { setBusy(false); }
    }
    return {state, error, busy, message, refresh, run};
}

function Feedback({error, message}) {
    return <>
        {error && <p role="alert" className="mt-4 rounded-xl border border-amber-900 bg-amber-950/20 p-3 text-sm text-amber-200">{error}</p>}
        {message && <p role="status" className="mt-4 text-sm text-zinc-300">{message}</p>}
    </>;
}

export function GitHubProxyPage({onCopy, onSettings}) {
    const {state, error, busy, message, refresh, run} = useProxy();
    const base = state?.addresses?.[0]?.url;
    const npmAddresses = (state?.addresses || []).map(address => ({...address, url: address.url.replace(/\/github$/, '/npm/')}));
    const npmBase = npmAddresses[0]?.url;
    const npmExamples = npmBase ? [
        ['安装公开包（仅本次命令）', `npm install lodash --registry=${npmBase}`],
        ['安装作用域包', `npm install @types/node --registry=${npmBase}`],
        ['从锁文件安装', `npm ci --registry=${npmBase}`],
        ['检查连通性', `npm ping --registry=${npmBase}`],
        ['审计项目依赖', `npm audit --registry=${npmBase}`],
        ['当前项目 .npmrc 配置', `registry=${npmBase}`],
    ] : [];
    const examples = base ? [
        ['克隆仓库', `git clone ${base}/octocat/Hello-World.git`],
        ['下载源码归档', `${base}/octocat/Hello-World/archive/refs/heads/master.zip`],
        ['下载 Release 资产', `${base}/OWNER/REPO/releases/download/TAG/FILE`],
        ['读取 raw 文件', `${base}/octocat/Hello-World/raw/master/README`],
        ['当前仓库自动替换 GitHub 地址', `git config --local url."${base}/".insteadOf "https://github.com/"`],
        ['撤销当前仓库的地址替换', `git config --local --remove-section 'url.${base}/'`],
    ] : [];
    return <main className="min-h-0 flex-1 overflow-y-auto px-7 py-5">
        <div className="mx-auto max-w-4xl">
            <section className="mt-7 rounded-2xl border border-zinc-800 bg-zinc-950/30 p-5">
                <div className="flex flex-wrap items-start justify-between gap-4">
                    <div><p className="text-xs font-semibold uppercase tracking-widest text-emerald-400">GitHub + npm / 资源转发</p><h2 className="mt-2 text-2xl font-bold">让仓库与依赖连接更简单</h2><p className="mt-2 text-sm text-zinc-500">GitHub 克隆与下载、npm 公开包安装，共用端口和出站链路。</p></div>
                    <span className={`rounded-full border px-3 py-1 text-xs font-semibold ${state?.running ? 'border-emerald-800 bg-emerald-950/30 text-emerald-300' : 'border-zinc-700 text-zinc-400'}`}>{!state ? '正在读取状态…' : state.running ? '服务运行中' : state.settings.enabled ? '启动失败' : '服务已关闭'}</span>
                </div>
                <label className="mt-5 flex items-start justify-between gap-4 rounded-xl border border-zinc-800 bg-black/20 p-4">
                    <span><span className="block text-sm font-semibold">启用 资源代理</span><span className="mt-1 block text-xs leading-5 text-zinc-500">独立于 LLM 网关。关闭后释放端口；重启应用会保留开关状态。</span></span>
                    <input aria-label="启用 资源代理" className="mt-1 h-4 w-4 accent-emerald-500" type="checkbox" checked={Boolean(state?.settings.enabled)} disabled={!state || busy} onChange={e => { const enabled = e.target.checked; run(() => SetGitHubProxyEnabled(enabled), enabled ? '资源代理已开启。' : '资源代理已关闭。'); }} />
                </label>
                <div className="mt-4 flex flex-wrap items-center justify-between gap-3 text-sm"><span className="text-zinc-400">当前链路：{modes[state?.settings.config.mode] || '—'} · 端口 {state?.settings.config.port || '—'}</span><button type="button" onClick={onSettings} className="text-emerald-300 hover:text-emerald-200">配置端口与出站代理 →</button></div>
                <Feedback error={error || state?.error} message={message} />
                {state?.settings.enabled && !state.running && <button type="button" disabled={busy} onClick={() => run(() => SetGitHubProxyEnabled(true), '已重试启动。')} className="mt-3 rounded-lg border border-zinc-700 px-3 py-2 text-sm">重试启动</button>}
                {state && !state.running && <p className="mt-3 text-xs text-zinc-500">下方为配置地址，开启服务后可用。局域网客户端请使用对应网卡地址。</p>}
            </section>
            <section className="mt-5 rounded-2xl border border-zinc-800 bg-zinc-950/30 p-5">
                <h3 className="font-bold">GitHub 使用说明</h3>
                <AccessAddresses addresses={state?.addresses || []} onRefresh={refresh} onCopy={onCopy} />
                <p className="mt-2 text-sm leading-6 text-zinc-500">将 GitHub 地址的 https://github.com 替换为上方入口。仅支持公开仓库的读取，不支持推送、私有仓库认证或 Git LFS。</p>
                <div className="mt-4 space-y-4">{examples.map(([label, value]) => <div key={label}><p className="mb-2 text-xs text-zinc-400">{label}</p><div className="flex items-start gap-3 rounded-lg border border-zinc-800 bg-black/30 p-3"><code className="min-w-0 flex-1 break-all text-xs leading-6 text-zinc-200">{value}</code><button type="button" onClick={() => onCopy(value)} className="shrink-0 rounded border border-zinc-700 px-2 py-1 text-xs text-zinc-400 hover:text-white">复制</button></div></div>)}</div>
                <p className="mt-4 text-xs leading-6 text-zinc-500">insteadOf 示例在已有仓库目录执行，仅影响当前仓库，也会匹配 HTTPS 推送地址；写入请使用独立的直连 push URL。首次克隆请直接使用代理地址。自动模式优先直连，失败后尝试已配置代理；仓库链路缓存 5 分钟，失败链路缓存 30 秒。</p>
            </section>
            <section className="mt-5 rounded-2xl border border-zinc-800 bg-zinc-950/30 p-5">
                <h3 className="font-bold">npm 使用说明</h3>
                <p className="mt-2 text-sm leading-6 text-zinc-500">将 npm registry 设置为下方入口，支持公开包、作用域包、搜索与依赖审计。与 GitHub 共用服务开关和出站配置。</p>
                <AccessAddresses addresses={npmAddresses} onRefresh={refresh} onCopy={onCopy} />
                <div className="mt-4 space-y-4">{npmExamples.map(([label, value]) => <div key={label}><p className="mb-2 text-xs text-zinc-400">{label}</p><div className="flex items-start gap-3 rounded-lg border border-zinc-800 bg-black/30 p-3"><code className="min-w-0 flex-1 break-all text-xs leading-6 text-zinc-200">{value}</code><button type="button" onClick={() => onCopy(value)} className="shrink-0 rounded border border-zinc-700 px-2 py-1 text-xs text-zinc-400 hover:text-white">复制</button></div></div>)}</div>
                <p className="mt-4 text-xs leading-6 text-zinc-500">仅支持 npm 官方公开 registry，不支持登录、发布或私有包认证。安装脚本、Git 依赖及第三方下载地址不会自动经过此入口。旧锁文件中的其他镜像地址需自行调整；项目使用完毕可删除 .npmrc 中的 registry 配置恢复默认。</p>
            </section>
        </div>
    </main>;
}

export function GitHubProxySettings() {
    const {state, error, busy, message, run} = useProxy();
    const [draft, setDraft] = useState(null);
    const configJSON = state ? JSON.stringify(state.settings.config) : null;
    useEffect(() => { if (configJSON) setDraft(JSON.parse(configJSON)); }, [configJSON]);
    function update(patch) { setDraft(current => ({...current, ...patch})); }
    return <section className="mt-7 rounded-2xl border border-zinc-800 bg-zinc-950/30 p-5">
        <h2 className="text-xl font-bold">资源代理</h2>
        <p className="mt-1 text-sm text-zinc-500">独立保存端口与出站链路配置。服务运行时立即生效；修改端口会结束在途下载。</p>
        {draft && <form className="mt-5" onSubmit={e => { e.preventDefault(); run(() => SaveGitHubProxyConfig({...draft, port: Number(draft.port), proxyAddress: draft.proxyAddress.trim()}), '资源代理设置已保存。'); }}>
            <fieldset disabled={busy} className="grid gap-4 sm:grid-cols-2 disabled:opacity-60">
                <label className="text-sm text-zinc-300">监听端口<input className={control} type="number" required min="1" max="65535" value={draft.port} onChange={e => update({port: e.target.value})} /></label>
                <label className="text-sm text-zinc-300">链路模式<select className={control} value={draft.mode} onChange={e => update({mode: e.target.value})}>{Object.entries(modes).map(([key,label]) => <option key={key} value={key}>{label}</option>)}</select></label>
                <label className="text-sm text-zinc-300">代理类型<select className={control} value={draft.proxyType} onChange={e => update({proxyType: e.target.value})}><option value="http">HTTP</option><option value="socks5">SOCKS5</option></select></label>
                <label className="text-sm text-zinc-300">代理地址<input className={control} placeholder="127.0.0.1:7890" required={draft.mode === 'proxy'} value={draft.proxyAddress} onChange={e => update({proxyAddress: e.target.value})} /></label>
            </fieldset>
            <p className="mt-3 text-xs leading-6 text-zinc-500">地址格式为 host:port，支持 IPv6 [::1]:7890；当前支持无需认证的代理。自动模式未配置代理时使用直连。仅直连模式忽略出站代理配置。</p>
            <button disabled={busy} className="mt-4 rounded-xl bg-zinc-100 px-5 py-2.5 font-bold text-zinc-950 hover:bg-white disabled:opacity-60" type="submit">{busy ? '正在保存…' : '保存 资源代理设置'}</button>
        </form>}
        <Feedback error={error} message={message} />
    </section>;
}
