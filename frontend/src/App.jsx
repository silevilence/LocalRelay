import {useState} from 'react';
import {Greet} from "../wailsjs/go/main/App";

function App() {
    const [resultText, setResultText] = useState("后端已绑定，输入名称试试。");
    const [name, setName] = useState('LocalRelay');

    function greet() {
        Greet(name).then(setResultText);
    }

    return (
        <div className="min-h-screen bg-slate-950 text-slate-100">
            <div className="mx-auto flex min-h-screen max-w-4xl flex-col justify-center px-8">
                <p className="mb-4 text-sm font-semibold uppercase tracking-[0.3em] text-cyan-300">LocalRelay</p>
                <h1 className="text-5xl font-bold tracking-tight">本地 LLM 中转网关</h1>
                <p className="mt-6 max-w-2xl text-lg text-slate-300">
                    Wails v2 + Go + React + Tailwind 已接通，下一步可以开始做 Provider 和 Model 管理。
                </p>

                <div className="mt-10 flex max-w-xl gap-3">
                    <input
                        className="min-w-0 flex-1 rounded-xl border border-slate-700 bg-slate-900 px-4 py-3 text-slate-100 outline-none ring-cyan-400/40 placeholder:text-slate-500 focus:ring-4"
                        value={name}
                        onChange={(event) => setName(event.target.value)}
                        autoComplete="off"
                    />
                    <button
                        className="rounded-xl bg-cyan-400 px-5 py-3 font-semibold text-slate-950 transition hover:bg-cyan-300"
                        onClick={greet}
                    >
                        验证后端
                    </button>
                </div>

                <div className="mt-4 rounded-xl border border-slate-800 bg-slate-900/70 px-4 py-3 text-slate-300">
                    {resultText}
                </div>
            </div>
        </div>
    )
}

export default App
