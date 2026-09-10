import { useNavigate } from '@tanstack/react-router';
import { Card, Col, Row, Statistic, Tooltip, Typography } from 'antd';
import type { AccountStatus, Overview } from '@/api/types';
import { ACCOUNT_STATUS_META } from '@/constants/account';

interface StatusSummaryProps {
  data: Overview;
}

const ORDER: AccountStatus[] = ['ACTIVE', 'EXPIRING', 'UNVERIFIED', 'INVALID'];

/** 状态分布卡片组: 总量 + 四种状态计数, 点击可跳转到对应筛选后的账号列表 */
export function StatusSummary({ data }: StatusSummaryProps) {
  const navigate = useNavigate();
  const total = data.total ?? 0;
  // 账号上了规模之后总数是估算的, 分状态计数会在上限处截断。
  // 把这件事显式标出来, 而不是让一个约数装成精确值 ——
  // 用户拿这个数去核对导入结果时, 差几个会被当成系统丢了账号。
  const totalExact = data.total_exact !== false;
  const statusCapped = data.by_status_capped === true;

  /** 跳到按状态筛选的账号列表 */
  const goToAccounts = (status?: AccountStatus) => {
    void navigate({ to: '/accounts', search: { status, page: 1 } });
  };

  return (
    <Row gutter={[16, 16]} align="stretch">
      <Col xs={24} sm={12} lg={4} xl={4}>
        {/* okc-lift 只加过渡, 悬浮抬起 2px; hoverable 负责 antd 自带的阴影变化 */}
        <Card
          hoverable
          className="okc-pop okc-lift"
          onClick={() => goToAccounts(undefined)}
          style={{ height: '100%' }}
          styles={{ body: { padding: 16 } }}
        >
          <Tooltip title={totalExact ? undefined : '账号量已超过精确统计的规模, 这里显示的是数据库的行数估计, 误差通常在几个百分点'}>
            <Statistic title="账号总数" value={total} prefix={totalExact ? undefined : '约'} />
          </Tooltip>
        </Card>
      </Col>
      {ORDER.map((status, index) => {
        const meta = ACCOUNT_STATUS_META[status];
        const count = data.by_status?.[status] ?? 0;
        const percent = total > 0 ? Math.round((count / total) * 1000) / 10 : 0;
        return (
          <Col key={status} xs={12} sm={12} lg={5} xl={5}>
            <Card
              hoverable
              className="okc-pop okc-lift"
              onClick={() => goToAccounts(status)}
              // 四张状态卡依次弹出, 与左边的总数卡拉开先后
              style={{ height: '100%', animationDelay: `${(index + 1) * 55}ms` }}
              styles={{ body: { padding: 16 } }}
            >
              <Statistic
                suffix={statusCapped && count > 0 ? '+' : undefined}
                title={
                  <span>
                    <span
                      aria-hidden
                      style={{
                        display: 'inline-block',
                        width: 8,
                        height: 8,
                        borderRadius: 2,
                        marginInlineEnd: 6,
                        background: meta.hex,
                      }}
                    />
                    {meta.label}
                  </span>
                }
                value={count}
                valueStyle={{ color: meta.hex }}
              />
              {statusCapped ? null : (
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                  占比 {percent}%
                </Typography.Text>
              )}
            </Card>
          </Col>
        );
      })}
    </Row>
  );
}
