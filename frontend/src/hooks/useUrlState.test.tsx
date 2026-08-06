// @vitest-environment jsdom
//
// useUrlState 的**同批次多次 URL 写入必须叠加**守卫。
//
// 背景：react-router 的 setSearchParams(prev => ...) 里的 prev 是闭包捕获的上一次渲染值，
// 且每次调用立刻 navigate——不是 React setState 那种把上一个 updater 结果喂给下一个的队列。
// 所以同一个事件里连着调多个 setter 时，后面的会拿着旧值再导航一遍，把前面的改动抹掉。
// 现网踩到的入口是移动端筛选抽屉的「清空」按钮：它一次调 6 个 setter，实际只有最后一个生效。
//
// 这两个用例分别钉住修复的两半：
//   ① 同批次叠加——六个 setter 连着调，六个参数都得没；
//   ② 跨批次不粘连——上一批次的基底不能残留到下一批次，否则被删掉的参数会诈尸。
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { describe, it, expect, afterEach } from 'vitest';
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom';
import { useUrlState } from './useUrlState';

type Actions = ReturnType<typeof useUrlState>[1];

/** 每个用例挂载的 root 卸载钩子——避免残留 root 干扰后续用例。 */
const mounted: Array<() => void> = [];
afterEach(() => {
  while (mounted.length) mounted.pop()!();
  sessionStorage.clear();
});

/** 把 hook 的 actions、当前 URL search、以及一个绕开 hook 的导航口暴露给用例。 */
function harness(initialSearch: string): {
  actions: () => Actions;
  search: () => string;
  navigateExternally: (to: string) => void;
} {
  let latestActions: Actions;
  let latestSearch = '';
  let latestNavigate: (to: string) => void = () => {};

  function Probe() {
    const [, actions] = useUrlState();
    latestActions = actions;
    latestSearch = useLocation().search;
    const navigate = useNavigate();
    latestNavigate = (to: string) => navigate(to, { replace: true });
    return null;
  }

  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(
      <MemoryRouter initialEntries={[`/${initialSearch}`]}>
        <Probe />
      </MemoryRouter>,
    );
  });

  mounted.push(() => act(() => root.unmount()));

  return {
    actions: () => latestActions,
    search: () => latestSearch,
    navigateExternally: (to: string) => act(() => latestNavigate(to)),
  };
}

describe('useUrlState 同批次多次写入', () => {
  it('收藏模式下点「清空」：退出收藏恢复出来的筛选也必须一并清干净', () => {
    // 复刻真实入口：Controls 抽屉「清空」的第一步 onShowFavoritesOnlyChange(false)
    // 在 App 里落到 exitFavoritesMode()，而它会先把快照里的筛选**恢复进 URL**，
    // 紧接着后面五个 setter 再逐个清掉。这一恢复一清是最容易被覆盖冲掉的形状。
    sessionStorage.setItem('relay-pulse:v1:list-state', JSON.stringify({
      version: 1,
      filterProvider: ['acme'],
      filterService: ['cc'],
      filterChannel: ['vip'],
      filterCategory: ['public'],
      filterVendor: ['openai'],
    }));
    const h = harness('?fav=1&period=7d');

    act(() => {
      const a = h.actions();
      a.exitFavoritesMode();      // 删 fav + 清筛选 + 恢复快照筛选
      a.setFilterCategory([]);
      a.setFilterProvider([]);
      a.setFilterService([]);
      a.setFilterChannel([]);
      a.setFilterVendor([]);
    });

    const params = new URLSearchParams(h.search());
    for (const key of ['provider', 'service', 'channel', 'category', 'vendor', 'fav']) {
      expect({ key, value: params.get(key) }).toEqual({ key, value: null });
    }
    // 不相干的参数不能被误伤
    expect(params.get('period')).toBe('7d');
  });

  it('跨批次不粘连：批次基底不能跨到下一批，否则会盖掉期间的外部导航', async () => {
    const h = harness('?provider=acme');

    // 批次 1：正常写入，批次基底 = {provider=acme, service=cc}
    act(() => {
      h.actions().setFilterService(['cc']);
    });
    expect(new URLSearchParams(h.search()).get('service')).toBe('cc');

    // 批次边界：让微任务队列排空。浏览器里这是天然的——点击处理函数、
    // 一次 commit 的 passive effect 各自跑完就会排空；同步的测试体里不会，得显式让出。
    await Promise.resolve();

    // 期间发生一次不经本 hook 的导航（浏览器前进后退、别处的 useSearchParams、<Link> 都算）
    h.navigateExternally('/?period=30d');
    expect(h.search()).toBe('?period=30d');

    // 批次 2：只想加个 channel。若批次 1 的基底残留，provider/service 会诈尸、period 被抹掉
    act(() => {
      h.actions().setFilterChannel(['vip']);
    });

    const params = new URLSearchParams(h.search());
    expect({
      channel: params.get('channel'),
      period: params.get('period'),
      provider: params.get('provider'),
      service: params.get('service'),
    }).toEqual({ channel: 'vip', period: '30d', provider: null, service: null });
  });
});
