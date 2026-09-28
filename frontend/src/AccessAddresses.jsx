export default function AccessAddresses({addresses, onRefresh, onCopy}) {
    return <div className="mt-5 rounded-xl border border-zinc-800 bg-black/20 p-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
                <h3 className="font-bold text-zinc-200">本机访问地址</h3>
                <p className="mt-1 text-xs text-zinc-500">端口取当前设置；虚拟网卡也会列出。请确保 Windows 防火墙允许对应入站连接。</p>
            </div>
            <button className="rounded-lg border border-zinc-700 px-3 py-1.5 text-sm text-zinc-300 hover:bg-zinc-900" type="button" onClick={onRefresh}>刷新</button>
        </div>
        <div className="mt-3 divide-y divide-zinc-800 overflow-hidden rounded-lg border border-zinc-800">
            {addresses.map((address) => <div key={`${address.url}-${address.source}`} className="flex flex-wrap items-center gap-2 bg-zinc-950/40 px-3 py-2.5 text-sm">
                <code className="min-w-0 flex-1 break-all font-mono text-zinc-200">{address.url}</code>
                <span className="text-xs text-zinc-500">({address.source})</span>
                <button className="rounded-lg border border-zinc-700 px-2.5 py-1 text-xs text-zinc-300 hover:bg-zinc-900" type="button" onClick={() => onCopy(address.url)}>⧉ 复制</button>
            </div>)}
            {!addresses.length && <div className="px-3 py-3 text-sm text-zinc-500">正在读取本机网络地址…</div>}
        </div>
    </div>;
}
