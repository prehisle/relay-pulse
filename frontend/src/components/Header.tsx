import { Activity, CheckCircle, AlertTriangle } from 'lucide-react';

interface HeaderProps {
  stats: {
    total: number;
    healthy: number;
    issues: number;
  };
}

export function Header({ stats }: HeaderProps) {
  return (
    <header className="flex flex-col md:flex-row justify-between items-start md:items-center mb-10 gap-4 border-b border-slate-800/50 pb-6">
      <div>
        <div className="flex items-center gap-3 mb-2">
          <div className="p-2 bg-cyan-500/10 rounded-lg border border-cyan-500/20">
            <Activity className="w-6 h-6 text-cyan-400" />
          </div>
          <h1 className="text-3xl font-bold bg-clip-text text-transparent bg-gradient-to-r from-cyan-400 via-blue-400 to-purple-400">
            Service Horizon
          </h1>
        </div>
        <p className="text-slate-400 text-sm flex items-center gap-2">
          <span className="inline-block w-2 h-2 rounded-full bg-emerald-500 animate-pulse"></span>
          实时监控服务可用性矩阵
        </p>
      </div>

      <div className="flex gap-4 text-sm">
        <div className="px-4 py-2 rounded-xl bg-slate-900/50 border border-slate-800 backdrop-blur-sm flex items-center gap-3 shadow-lg">
          <div className="p-1.5 rounded-full bg-emerald-500/10 text-emerald-400">
            <CheckCircle size={16} />
          </div>
          <div>
            <div className="text-slate-400 text-xs">正常运行</div>
            <div className="font-mono font-bold text-emerald-400">{stats.healthy}</div>
          </div>
        </div>
        <div className="px-4 py-2 rounded-xl bg-slate-900/50 border border-slate-800 backdrop-blur-sm flex items-center gap-3 shadow-lg">
          <div className="p-1.5 rounded-full bg-rose-500/10 text-rose-400">
            <AlertTriangle size={16} />
          </div>
          <div>
            <div className="text-slate-400 text-xs">异常告警</div>
            <div className="font-mono font-bold text-rose-400">{stats.issues}</div>
          </div>
        </div>
      </div>
    </header>
  );
}
