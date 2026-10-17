import * as echarts from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { DataZoomComponent, GridComponent, LegendComponent, TooltipComponent, MarkAreaComponent, MarkLineComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

echarts.use([LineChart, DataZoomComponent, GridComponent, LegendComponent, TooltipComponent, MarkAreaComponent, MarkLineComponent, CanvasRenderer])

export default echarts
