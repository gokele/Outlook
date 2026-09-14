import { ApiOutlined, CodeOutlined, ImportOutlined } from '@ant-design/icons';
import { Link } from '@tanstack/react-router';
import { Button, Card, Flex, Typography, theme } from 'antd';
import type { ReactNode } from 'react';

interface Step {
  icon: ReactNode;
  title: string;
  /** 说清这一步在干什么, 以及为什么不必担心 */
  body: ReactNode;
  to: '/import' | '/apikeys';
  cta: string;
}

/**
 * 池子还空着时的三步引导。
 *
 * 原来这里渲染的是一屏全零的仪表盘：账号总数 0、正常 0、失效 0、成功率 —— ，
 * 没有任何一处告诉人下一步该做什么。而同一个产品里已经有正确答案：账号列表
 * 的空态写着格式、给了「去导入账号」的按钮 —— 两个空态两套标准，
 * 偏偏用户先看到的是没做的那个。
 *
 * 编号在这里是有意义的：这三步确实有先后，密钥要等池子里有账号才有东西可取，
 * 示例请求要等密钥建好才跑得通。不是拿数字当装饰。
 */
const STEPS: Step[] = [
  {
    icon: <ImportOutlined />,
    title: '导入账号',
    body: (
      <>
        每行一个，按 <Typography.Text code>邮箱----密码----clientid----授权码</Typography.Text>{' '}
        粘贴进来即可，也可以直接传文件。这四段通常由账号提供方一并给出，不需要自己去微软申请。
        导入时<Typography.Text strong>不会联网验证</Typography.Text>，不用担心触发风控。
      </>
    ),
    to: '/import',
    cta: '去导入',
  },
  {
    icon: <ApiOutlined />,
    title: '建一把 API 密钥',
    body: <>外部程序靠它调用取件接口。可以限定分类范围、限速、绑 IP 白名单。</>,
    to: '/apikeys',
    cta: '去创建',
  },
  {
    icon: <CodeOutlined />,
    title: '发第一个请求',
    body: (
      <>
        密钥页右上角的「调用示例」里有现成的 curl，换上自己的邮箱就能跑。
        第一条是<Typography.Text strong>立刻返回</Typography.Text>的，
        取回此刻邮箱里最新的一封。
      </>
    ),
    to: '/apikeys',
    cta: '看示例',
  },
];

/** 池子为空时替代整个看板：先告诉人下一步做什么，而不是摆一屏零 */
export function GettingStarted() {
  const { token } = theme.useToken();

  return (
    <Card>
      <Typography.Title level={5} style={{ marginTop: 0 }}>
        账号池还是空的，从这三步开始
      </Typography.Title>
      <Typography.Paragraph type="secondary" style={{ marginBottom: 24 }}>
        导入之后这里会变成账号池的看板：状态分布、取件成功率、调度器健康度。
      </Typography.Paragraph>

      <Flex vertical gap={20}>
        {STEPS.map((step, i) => (
          <Flex key={step.title} gap={16} align="flex-start">
            {/*
              序号与图标合成一个圆：序号说明先后，图标说明是哪一类操作。
              两者分开摆会让每一行前面顶着两个小东西，反而更碎。
            */}
            <Flex
              align="center"
              justify="center"
              style={{
                flex: 'none',
                width: 36,
                height: 36,
                borderRadius: '50%',
                background: token.colorFillTertiary,
                color: token.colorTextSecondary,
                fontSize: 16,
              }}
            >
              {step.icon}
            </Flex>
            <Flex vertical gap={6} style={{ minWidth: 0, flex: '1 1 auto' }}>
              <Typography.Text strong>
                <Typography.Text type="secondary" style={{ fontWeight: 400 }}>
                  {i + 1}.{' '}
                </Typography.Text>
                {step.title}
              </Typography.Text>
              <Typography.Paragraph type="secondary" style={{ margin: 0, fontSize: 13 }}>
                {step.body}
              </Typography.Paragraph>
              <div>
                <Link to={step.to}>
                  <Button type={i === 0 ? 'primary' : 'default'} size="small">
                    {step.cta}
                  </Button>
                </Link>
              </div>
            </Flex>
          </Flex>
        ))}
      </Flex>
    </Card>
  );
}
