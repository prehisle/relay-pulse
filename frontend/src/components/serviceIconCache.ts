// 服务图标的模块级缓存：状态表每行、移动端每张卡片、网格每张卡片都要取一次，
// 而生产只有 cc/cx/gm 三种 serviceType，命中率接近 100%。
//
// 单独成文件而不是并入 ServiceIcon.tsx：那个文件只导出图标组件，混进模块级可变
// 状态会让它失去整文件热替换（eslint react-refresh 规则正是在防这个），
// 热替换时缓存还会被重置成两份。此前 StatusTable 与 StatusCard 各抄了一份实现。
import { getServiceIconComponent } from './ServiceIcon';

type ServiceIconComponent = ReturnType<typeof getServiceIconComponent>;

const cache = new Map<string, ServiceIconComponent>();

export function getCachedServiceIcon(serviceType: string): ServiceIconComponent {
  if (!cache.has(serviceType)) {
    cache.set(serviceType, getServiceIconComponent(serviceType));
  }
  return cache.get(serviceType) ?? null;
}
